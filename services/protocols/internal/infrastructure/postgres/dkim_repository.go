package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/infrastructure/vault"
)

// DkimRepository stores DKIM key pairs with the private half sealed. The
// vault lives here rather than in the application layer so no use case ever
// handles ciphertext (PLAN.md section 9).
type DkimRepository struct {
	pool  *pgxpool.Pool
	vault *vault.SecretVault
}

func NewDkimRepository(pool *pgxpool.Pool, v *vault.SecretVault) *DkimRepository {
	return &DkimRepository{pool: pool, vault: v}
}

func (r *DkimRepository) FindActiveKeys(ctx context.Context, domainName string) ([]port.DkimSigningKey, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT k.selector, k.algorithm, k.private_key_enc, k.private_key_nonce, k.key_version
		FROM dkim_keys k
		JOIN domains d ON d.id = k.domain_id
		WHERE d.name = $1 AND d.is_active AND k.status = 'ACTIVE'
		ORDER BY k.algorithm
	`, domainName)
	if err != nil {
		return nil, fmt.Errorf("query active dkim keys for %s: %w", domainName, err)
	}
	defer rows.Close()

	keys := make([]port.DkimSigningKey, 0, 2)
	for rows.Next() {
		var (
			selector, algorithm string
			enc, nonce          []byte
			keyVersion          int
		)
		if err := rows.Scan(&selector, &algorithm, &enc, &nonce, &keyVersion); err != nil {
			return nil, fmt.Errorf("scan dkim key: %w", err)
		}

		privateKey, err := r.vault.Open(enc, nonce, keyVersion)
		if err != nil {
			return nil, fmt.Errorf("open dkim key %s for %s: %w", selector, domainName, err)
		}

		keys = append(keys, port.DkimSigningKey{
			DomainName:    domainName,
			Selector:      selector,
			Algorithm:     algorithm,
			PrivateKeyPEM: privateKey,
		})
	}

	return keys, rows.Err()
}

// FindPublicKey returns the published half of an active key, which the DNS
// reconciler needs to build the "p=" tag. It returns an empty string when the
// domain has no active key of that algorithm.
func (r *DkimRepository) FindPublicKey(ctx context.Context, domainName, algorithm string) (string, error) {
	var publicKey string
	err := r.pool.QueryRow(ctx, `
		SELECT k.public_key
		FROM dkim_keys k
		JOIN domains d ON d.id = k.domain_id
		WHERE d.name = $1 AND k.algorithm = $2 AND k.status = 'ACTIVE'
	`, domainName, algorithm).Scan(&publicKey)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query public dkim key for %s: %w", domainName, err)
	}
	return publicKey, nil
}

// Insert stores a new key pair, sealing the private half. It is a no-op when
// the domain already has an active key of that algorithm, which keeps
// provisioning idempotent and upholds the "one ACTIVE key per (domain,
// algorithm)" invariant without racing the unique partial index.
func (r *DkimRepository) Insert(ctx context.Context, domainName, selector, algorithm string, privateKeyPEM []byte, publicKey string) error {
	ciphertext, nonce, err := r.vault.Seal(privateKeyPEM)
	if err != nil {
		return fmt.Errorf("seal dkim private key: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO dkim_keys (
			domain_id, selector, algorithm, private_key_enc, private_key_nonce,
			key_version, public_key, status, activated_at
		)
		SELECT d.id, $2, $3::varchar, $4, $5, $6, $7, 'ACTIVE', NOW()
		FROM domains d
		WHERE d.name = $1
		  AND NOT EXISTS (
		      SELECT 1 FROM dkim_keys existing
		      WHERE existing.domain_id = d.id
		        -- Cast explicitly: without it Postgres infers this parameter
		        -- as text here and as varchar in the insert list, and refuses
		        -- the statement.
		        AND existing.algorithm = $3::varchar
		        AND existing.status = 'ACTIVE'
		  )
	`, domainName, selector, algorithm, ciphertext, nonce, vault.CurrentKeyVersion, publicKey)

	if err != nil {
		return fmt.Errorf("insert dkim key for %s: %w", domainName, err)
	}
	return nil
}

// Ensure port interface compliance
var _ port.DkimKeyRepository = (*DkimRepository)(nil)
