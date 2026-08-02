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

// AcmeRepository stores self-managed ACME state with every private key sealed
// (PLAN.md section 8.3).
type AcmeRepository struct {
	pool         *pgxpool.Pool
	vault        *vault.SecretVault
	directoryURL string
}

func NewAcmeRepository(pool *pgxpool.Pool, v *vault.SecretVault, directoryURL string) *AcmeRepository {
	return &AcmeRepository{pool: pool, vault: v, directoryURL: directoryURL}
}

func (r *AcmeRepository) LoadCertificate(ctx context.Context, domain string) (*port.StoredCertificate, error) {
	var (
		cert          port.StoredCertificate
		certPEM       string
		enc, nonce    []byte
		keyVersion    int
		keyGeneration int
		notAfter      time.Time
	)

	err := r.pool.QueryRow(ctx, `
		SELECT certificate_pem, private_key_enc, private_key_nonce, key_version, key_generation, not_after
		FROM acme_certificates WHERE domain = $1
	`, domain).Scan(&certPEM, &enc, &nonce, &keyVersion, &keyGeneration, &notAfter)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load certificate for %s: %w", domain, err)
	}

	privateKey, err := r.vault.Open(enc, nonce, keyVersion)
	if err != nil {
		return nil, fmt.Errorf("open certificate key for %s: %w", domain, err)
	}

	cert.Domain = domain
	cert.CertificatePEM = []byte(certPEM)
	cert.PrivateKeyPEM = privateKey
	cert.KeyGeneration = keyGeneration
	cert.NotAfter = notAfter
	return &cert, nil
}

func (r *AcmeRepository) SaveCertificate(ctx context.Context, cert port.StoredCertificate) error {
	enc, nonce, err := r.vault.Seal(cert.PrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("seal certificate key: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO acme_certificates (
			domain, certificate_pem, private_key_enc, private_key_nonce,
			key_version, key_generation, not_after
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (domain) DO UPDATE SET
			certificate_pem   = EXCLUDED.certificate_pem,
			private_key_enc   = EXCLUDED.private_key_enc,
			private_key_nonce = EXCLUDED.private_key_nonce,
			key_version       = EXCLUDED.key_version,
			key_generation    = EXCLUDED.key_generation,
			not_after         = EXCLUDED.not_after,
			updated_at        = NOW()
	`, cert.Domain, string(cert.CertificatePEM), enc, nonce, vault.CurrentKeyVersion, cert.KeyGeneration, cert.NotAfter)

	if err != nil {
		return fmt.Errorf("save certificate for %s: %w", cert.Domain, err)
	}
	return nil
}

func (r *AcmeRepository) LoadAccountKey(ctx context.Context) ([]byte, error) {
	var (
		enc, nonce []byte
		keyVersion int
	)

	err := r.pool.QueryRow(ctx, `
		SELECT private_key_enc, private_key_nonce, key_version
		FROM acme_accounts WHERE directory_url = $1
	`, r.directoryURL).Scan(&enc, &nonce, &keyVersion)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load acme account key: %w", err)
	}

	return r.vault.Open(enc, nonce, keyVersion)
}

func (r *AcmeRepository) SaveAccountKey(ctx context.Context, privateKeyPEM []byte) error {
	enc, nonce, err := r.vault.Seal(privateKeyPEM)
	if err != nil {
		return fmt.Errorf("seal acme account key: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO acme_accounts (directory_url, private_key_enc, private_key_nonce, key_version)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (directory_url) DO UPDATE SET
			private_key_enc   = EXCLUDED.private_key_enc,
			private_key_nonce = EXCLUDED.private_key_nonce,
			key_version       = EXCLUDED.key_version
	`, r.directoryURL, enc, nonce, vault.CurrentKeyVersion)

	if err != nil {
		return fmt.Errorf("save acme account key: %w", err)
	}
	return nil
}

func (r *AcmeRepository) LoadPendingKey(ctx context.Context, domain string) ([]byte, error) {
	var (
		enc, nonce []byte
		keyVersion int
	)

	err := r.pool.QueryRow(ctx, `
		SELECT private_key_enc, private_key_nonce, key_version
		FROM acme_pending_keys WHERE domain = $1
	`, domain).Scan(&enc, &nonce, &keyVersion)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load pending key for %s: %w", domain, err)
	}

	return r.vault.Open(enc, nonce, keyVersion)
}

// SavePendingKey stores the key reserved for the next issuance together with
// its TLSA digest, so the rollover can publish the association without having
// to re-derive it.
func (r *AcmeRepository) SavePendingKey(ctx context.Context, domain string, privateKeyPEM []byte) error {
	return r.SavePendingKeyWithDigest(ctx, domain, privateKeyPEM, "")
}

func (r *AcmeRepository) SavePendingKeyWithDigest(ctx context.Context, domain string, privateKeyPEM []byte, tlsaDigest string) error {
	enc, nonce, err := r.vault.Seal(privateKeyPEM)
	if err != nil {
		return fmt.Errorf("seal pending key: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO acme_pending_keys (domain, private_key_enc, private_key_nonce, key_version, tlsa_sha256)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (domain) DO UPDATE SET
			private_key_enc   = EXCLUDED.private_key_enc,
			private_key_nonce = EXCLUDED.private_key_nonce,
			key_version       = EXCLUDED.key_version,
			tlsa_sha256       = EXCLUDED.tlsa_sha256,
			published_at      = NULL
	`, domain, enc, nonce, vault.CurrentKeyVersion, tlsaDigest)

	if err != nil {
		return fmt.Errorf("save pending key for %s: %w", domain, err)
	}
	return nil
}

// PendingKeyDigest returns the TLSA digest of the reserved key and when its
// association was published, or empty values when there is no pending key.
func (r *AcmeRepository) PendingKeyDigest(ctx context.Context, domain string) (digest string, publishedAt *time.Time, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT tlsa_sha256, published_at FROM acme_pending_keys WHERE domain = $1
	`, domain).Scan(&digest, &publishedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("load pending key digest for %s: %w", domain, err)
	}
	return digest, publishedAt, nil
}

// MarkPendingKeyPublished records that the association is now in DNS, which
// starts the propagation wait.
func (r *AcmeRepository) MarkPendingKeyPublished(ctx context.Context, domain string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE acme_pending_keys SET published_at = NOW() WHERE domain = $1 AND published_at IS NULL
	`, domain)
	if err != nil {
		return fmt.Errorf("mark pending key published for %s: %w", domain, err)
	}
	return nil
}

func (r *AcmeRepository) ClearPendingKey(ctx context.Context, domain string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM acme_pending_keys WHERE domain = $1`, domain)
	if err != nil {
		return fmt.Errorf("clear pending key for %s: %w", domain, err)
	}
	return nil
}

// Ensure port interface compliance
var _ port.CertificateStore = (*AcmeRepository)(nil)
