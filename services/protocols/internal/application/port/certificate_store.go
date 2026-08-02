package port

import (
	"context"
	"time"
)

// StoredCertificate is one issued certificate together with the key it was
// issued for. PLAN.md section 8.3 keeps these in Postgres so that recreating
// the container does not lose the account or force a re-issuance, which would
// quickly hit the CA's rate limits.
type StoredCertificate struct {
	Domain string
	// CertificatePEM is the full chain, leaf first.
	CertificatePEM []byte
	PrivateKeyPEM  []byte
	// NotAfter drives renewal scheduling.
	NotAfter time.Time
	// KeyGeneration increments on every key change. DANE needs it: the TLSA
	// record published for a generation must stay live until no certificate
	// of that generation is in use (RFC 7671 section 8.1).
	KeyGeneration int
}

// CertificateStore persists ACME state. Everything it holds is secret, so an
// implementation is expected to seal it at rest.
type CertificateStore interface {
	// LoadCertificate returns nil, nil when nothing is stored yet.
	LoadCertificate(ctx context.Context, domain string) (*StoredCertificate, error)
	SaveCertificate(ctx context.Context, cert StoredCertificate) error

	// LoadAccountKey returns the ACME account key, or nil when the deployment
	// has not registered with the CA yet.
	LoadAccountKey(ctx context.Context) ([]byte, error)
	SaveAccountKey(ctx context.Context, privateKeyPEM []byte) error

	// LoadPendingKey returns the pre-generated key reserved for the next
	// issuance. Pre-generating it is what makes a TLSA rollover possible: the
	// association for the next key can be published before the certificate
	// that uses it exists (PLAN.md section 5.1).
	LoadPendingKey(ctx context.Context, domain string) ([]byte, error)
	SavePendingKey(ctx context.Context, domain string, privateKeyPEM []byte) error
	ClearPendingKey(ctx context.Context, domain string) error
}
