package port

import "context"

// DkimSigningKey is one active signing key, already decrypted and ready to
// use. The repository owns the vault, so the application layer never handles
// sealed bytes.
type DkimSigningKey struct {
	DomainName    string
	Selector      string
	Algorithm     string
	PrivateKeyPEM []byte
}

// DkimKeyRepository serves the active signing keys for a domain. PLAN.md
// section 5 signs every outbound message with both an Ed25519 and an RSA key,
// so a domain normally yields two.
type DkimKeyRepository interface {
	// FindActiveKeys returns an empty slice (not an error) when the domain
	// has no active key: an unsigned message still beats a bounced one.
	FindActiveKeys(ctx context.Context, domainName string) ([]DkimSigningKey, error)
}

// DkimSigner prepends DKIM-Signature headers to a message.
type DkimSigner interface {
	// Sign returns the message with one signature per active key. A domain
	// with no keys yields the message unchanged.
	Sign(ctx context.Context, domainName string, message []byte) ([]byte, error)
}
