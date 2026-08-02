// Package dkim generates, stores and applies the DKIM signing keys described
// in PLAN.md section 5: every outbound message carries an Ed25519 and an
// RSA-2048 signature, because Ed25519 is smaller and faster but not every
// receiver validates it yet.
package dkim

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
)

// Algorithm names match the dkim_keys.algorithm check constraint
// (PLAN.md section 9).
const (
	AlgorithmRSA2048 = "rsa2048"
	AlgorithmEd25519 = "ed25519"
)

// GeneratedKey is a fresh key pair: the private half ready to be sealed into
// the vault, and the public half ready to be published as a DNS TXT record.
type GeneratedKey struct {
	Algorithm string
	// PrivateKeyPEM is PKCS#8, which covers both RSA and Ed25519 with one
	// encoding and so keeps the storage format uniform.
	PrivateKeyPEM []byte
	// PublicKeyBase64 is the bare base64 of the DER SubjectPublicKeyInfo for
	// RSA, and of the raw 32-byte key for Ed25519, which is what the "p=" tag
	// carries in each case (RFC 6376 section 3.6.1, RFC 8463 section 3).
	PublicKeyBase64 string
}

// Generate produces a key pair for one of the supported algorithms.
func Generate(algorithm string) (*GeneratedKey, error) {
	switch algorithm {
	case AlgorithmRSA2048:
		return generateRSA(2048)
	case AlgorithmEd25519:
		return generateEd25519()
	default:
		return nil, fmt.Errorf("dkim: unsupported algorithm %q", algorithm)
	}
}

func generateRSA(bits int) (*GeneratedKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, fmt.Errorf("generate rsa key: %w", err)
	}

	privPEM, err := marshalPrivatePEM(key)
	if err != nil {
		return nil, err
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal rsa public key: %w", err)
	}

	return &GeneratedKey{
		Algorithm:       AlgorithmRSA2048,
		PrivateKeyPEM:   privPEM,
		PublicKeyBase64: base64.StdEncoding.EncodeToString(pubDER),
	}, nil
}

func generateEd25519() (*GeneratedKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	privPEM, err := marshalPrivatePEM(priv)
	if err != nil {
		return nil, err
	}

	// RFC 8463 publishes the raw public key, not a SubjectPublicKeyInfo.
	return &GeneratedKey{
		Algorithm:       AlgorithmEd25519,
		PrivateKeyPEM:   privPEM,
		PublicKeyBase64: base64.StdEncoding.EncodeToString(pub),
	}, nil
}

func marshalPrivatePEM(key crypto.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParsePrivateKey reads back what marshalPrivatePEM wrote and returns it as a
// crypto.Signer, which is what the signing library consumes for either
// algorithm.
func ParsePrivateKey(pemBytes []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("dkim: private key is not valid PEM")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("dkim: parse private key: %w", err)
	}

	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("dkim: private key of type %T cannot sign", parsed)
	}
	return signer, nil
}
