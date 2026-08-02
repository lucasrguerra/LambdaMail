// Package vault seals the reusable secrets the system stores in Postgres -
// DKIM private keys and Cloudflare tokens (PLAN.md section 9). These are
// encrypted, never hashed: they have to be recoverable to be used at all.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// CurrentKeyVersion labels secrets sealed with the KEK derived below. Bumping
// it alongside a new derivation lets old rows stay readable during a rotation
// (PLAN.md section 9, *_key_version columns).
const CurrentKeyVersion = 1

// hkdfInfo binds the derived key to this purpose, so the same master key used
// elsewhere cannot produce the same KEK.
const hkdfInfo = "lambdamail/secret-vault/v1"

var (
	// ErrMasterKeyMissing is returned when the process has no master key. It
	// is fatal by design: continuing would mean writing secrets nobody can
	// decrypt later, or silently storing them in the clear.
	ErrMasterKeyMissing = errors.New("vault: LAMBDAMAIL_MASTER_KEY is not set")

	ErrUnknownKeyVersion = errors.New("vault: secret was sealed with an unknown key version")
)

// SecretVault seals and opens secrets with AES-256-GCM under a key derived
// from the operator's master key.
type SecretVault struct {
	aead cipher.AEAD
}

// New derives the key-encryption key from masterKey. The master key is passed
// through HKDF rather than used directly so that a short or low-entropy
// operator value still yields a full-length AES-256 key.
func New(masterKey string) (*SecretVault, error) {
	if masterKey == "" {
		return nil, ErrMasterKeyMissing
	}

	kek := make([]byte, 32)
	// A fixed salt keeps derivation deterministic across restarts, which is
	// required: a random salt would make every previously sealed secret
	// unreadable after a restart.
	salt := sha256.Sum256([]byte(hkdfInfo))
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(masterKey), salt[:], []byte(hkdfInfo)), kek); err != nil {
		return nil, fmt.Errorf("derive kek: %w", err)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	return &SecretVault{aead: aead}, nil
}

// Seal encrypts plaintext, returning the ciphertext and the nonce to store
// beside it. The nonce is stored separately because the schema models it as
// its own column (PLAN.md section 9).
func (v *SecretVault) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}
	return v.aead.Seal(nil, nonce, plaintext, nil), nonce, nil
}

// Open reverses Seal. A wrong master key surfaces here as an authentication
// failure rather than as silently corrupt plaintext, because GCM is
// authenticated.
func (v *SecretVault) Open(ciphertext, nonce []byte, keyVersion int) ([]byte, error) {
	if keyVersion != CurrentKeyVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnknownKeyVersion, keyVersion)
	}
	if len(nonce) != v.aead.NonceSize() {
		return nil, fmt.Errorf("vault: nonce is %d bytes, want %d", len(nonce), v.aead.NonceSize())
	}
	plaintext, err := v.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("vault: cannot open secret (wrong master key or corrupt data): %w", err)
	}
	return plaintext, nil
}
