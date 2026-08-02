package dkim

import (
	"bytes"
	"context"
	"fmt"
	"log"

	msgauth "github.com/emersion/go-msgauth/dkim"

	"lambdamail/protocols/internal/application/port"
)

// signedHeaders are the headers covered by the signature. Signing a header
// that is absent is harmless, but signing one that a forwarder may add later
// would break the signature, so the list stays close to RFC 6376 appendix D.
var signedHeaders = []string{
	"From", "To", "Cc", "Subject", "Date", "Message-ID",
	"MIME-Version", "Content-Type", "Content-Transfer-Encoding",
	"In-Reply-To", "References",
}

// Signer applies every active key of a domain to an outbound message.
type Signer struct {
	keys port.DkimKeyRepository
}

func NewSigner(keys port.DkimKeyRepository) *Signer {
	return &Signer{keys: keys}
}

// Sign adds one DKIM-Signature header per active key. PLAN.md section 5 signs
// with Ed25519 and RSA at the same time so that receivers which do not yet
// validate Ed25519 still see a passing RSA signature.
//
// A key that fails to sign is logged and skipped rather than failing the
// send: delivering with one signature, or none, beats not delivering.
func (s *Signer) Sign(ctx context.Context, domainName string, message []byte) ([]byte, error) {
	if s.keys == nil {
		return message, nil
	}

	keys, err := s.keys.FindActiveKeys(ctx, domainName)
	if err != nil {
		return nil, fmt.Errorf("load dkim keys for %s: %w", domainName, err)
	}

	signed := message
	for _, key := range keys {
		next, err := signOnce(signed, key)
		if err != nil {
			log.Printf("dkim: skipping %s signature for %s: %v", key.Algorithm, domainName, err)
			continue
		}
		signed = next
	}

	return signed, nil
}

func signOnce(message []byte, key port.DkimSigningKey) ([]byte, error) {
	signer, err := ParsePrivateKey(key.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}

	options := &msgauth.SignOptions{
		Domain:                 key.DomainName,
		Selector:               key.Selector,
		Signer:                 signer,
		HeaderKeys:             signedHeaders,
		HeaderCanonicalization: msgauth.CanonicalizationRelaxed,
		BodyCanonicalization:   msgauth.CanonicalizationRelaxed,
	}

	// Hash is left at its zero value on purpose: SHA-256 is the only hash
	// either algorithm admits (RFC 6376 for RSA, RFC 8463 for Ed25519), and
	// the library selects it per key type.
	var out bytes.Buffer
	if err := msgauth.Sign(&out, bytes.NewReader(message), options); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Ensure port interface compliance
var _ port.DkimSigner = (*Signer)(nil)
