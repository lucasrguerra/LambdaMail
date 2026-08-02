package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// DkimKeyInfo describes a key for the admin console. The private half is never
// part of this: it exists only to be used, not to be looked at.
type DkimKeyInfo struct {
	ID          string     `json:"id"`
	DomainName  string     `json:"domain"`
	Selector    string     `json:"selector"`
	Algorithm   string     `json:"algorithm"`
	PublicKey   string     `json:"public_key"`
	Status      string     `json:"status"`
	ActivatedAt *time.Time `json:"activated_at"`
	RetireAfter *time.Time `json:"retire_after"`
}

// ListKeys returns every key for a domain, newest first.
func (r *DkimRepository) ListKeys(ctx context.Context, domainName string) ([]DkimKeyInfo, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT k.id::text, d.name, k.selector, k.algorithm, k.public_key, k.status,
		       k.activated_at, k.retire_after
		  FROM dkim_keys k JOIN domains d ON d.id = k.domain_id
		 WHERE d.name = $1
		 ORDER BY k.created_at DESC
	`, domainName)
	if err != nil {
		return nil, fmt.Errorf("list dkim keys for %s: %w", domainName, err)
	}
	defer rows.Close()

	keys := []DkimKeyInfo{}
	for rows.Next() {
		var k DkimKeyInfo
		if err := rows.Scan(&k.ID, &k.DomainName, &k.Selector, &k.Algorithm,
			&k.PublicKey, &k.Status, &k.ActivatedAt, &k.RetireAfter); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Rotate replaces the active key for one algorithm.
//
// The old key is retired rather than deleted, and only after the new one is in
// place, both inside one transaction. Mail already in flight was signed with
// the old selector and receivers may still be resolving it, so removing it
// immediately would fail DKIM for messages that were perfectly valid when they
// were sent (PLAN.md section 5).
func (r *DkimRepository) Rotate(
	ctx context.Context, domainName, selector, algorithm string,
	privateKeyPEM []byte, publicKey string, overlap time.Duration,
) error {
	ciphertext, nonce, err := r.vault.Seal(privateKeyPEM)
	if err != nil {
		return fmt.Errorf("seal dkim private key: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var domainID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM domains WHERE name = $1`, domainName).Scan(&domainID); err != nil {
		return fmt.Errorf("find domain %s: %w", domainName, err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE dkim_keys SET status = 'RETIRING', retire_after = NOW() + $3::interval
		 WHERE domain_id = $1 AND algorithm = $2::varchar AND status = 'ACTIVE'
	`, domainID, algorithm, overlap.String()); err != nil {
		return fmt.Errorf("retire current dkim key: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO dkim_keys (
			domain_id, selector, algorithm, private_key_enc, private_key_nonce,
			key_version, public_key, status, activated_at
		) VALUES ($1, $2, $3::varchar, $4, $5, $6, $7, 'ACTIVE', NOW())
	`, domainID, selector, algorithm, ciphertext, nonce, vault.CurrentKeyVersion, publicKey); err != nil {
		return fmt.Errorf("insert rotated dkim key: %w", err)
	}

	return tx.Commit(ctx)
}
