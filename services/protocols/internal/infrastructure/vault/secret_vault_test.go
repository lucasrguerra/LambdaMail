package vault

import (
	"bytes"
	"errors"
	"testing"
)

func TestSecretVault_SealOpenRoundTrip(t *testing.T) {
	v, err := New("master-key-for-tests")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	secret := []byte("-----BEGIN PRIVATE KEY-----\nnot-really\n-----END PRIVATE KEY-----")
	ciphertext, nonce, err := v.Seal(secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(ciphertext, secret) {
		t.Error("plaintext is recoverable from the ciphertext")
	}

	opened, err := v.Open(ciphertext, nonce, CurrentKeyVersion)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, secret) {
		t.Errorf("round trip mismatch: got %q, want %q", opened, secret)
	}
}

// Sealing the same value twice must not produce the same ciphertext, or an
// observer with database access could tell which domains share a key.
func TestSecretVault_SealIsNonDeterministic(t *testing.T) {
	v, _ := New("master-key-for-tests")

	first, _, _ := v.Seal([]byte("same"))
	second, _, _ := v.Seal([]byte("same"))

	if bytes.Equal(first, second) {
		t.Error("sealing the same plaintext twice produced identical ciphertext")
	}
}

// The KEK must survive a restart, otherwise every stored secret becomes
// unreadable the first time the process is recreated.
func TestSecretVault_DerivationIsStableAcrossInstances(t *testing.T) {
	first, _ := New("master-key-for-tests")
	ciphertext, nonce, _ := first.Seal([]byte("payload"))

	second, _ := New("master-key-for-tests")
	opened, err := second.Open(ciphertext, nonce, CurrentKeyVersion)
	if err != nil {
		t.Fatalf("a freshly constructed vault could not open the secret: %v", err)
	}
	if string(opened) != "payload" {
		t.Errorf("got %q, want %q", opened, "payload")
	}
}

func TestSecretVault_WrongMasterKeyFails(t *testing.T) {
	good, _ := New("master-key-for-tests")
	ciphertext, nonce, _ := good.Seal([]byte("payload"))

	bad, _ := New("a-different-master-key")
	if _, err := bad.Open(ciphertext, nonce, CurrentKeyVersion); err == nil {
		t.Fatal("expected an authentication failure with the wrong master key")
	}
}

// Refusing to start without a master key beats writing secrets nobody can
// ever read back (PLAN.md risk R9).
func TestSecretVault_RequiresMasterKey(t *testing.T) {
	if _, err := New(""); !errors.Is(err, ErrMasterKeyMissing) {
		t.Fatalf("error = %v, want ErrMasterKeyMissing", err)
	}
}

func TestSecretVault_RejectsUnknownKeyVersion(t *testing.T) {
	v, _ := New("master-key-for-tests")
	ciphertext, nonce, _ := v.Seal([]byte("payload"))

	if _, err := v.Open(ciphertext, nonce, 99); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("error = %v, want ErrUnknownKeyVersion", err)
	}
}
