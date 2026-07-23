package valueobject

import "testing"

func TestNewPasswordHash_AcceptsArgon2idPHCString(t *testing.T) {
	phc := "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG"
	h, err := NewPasswordHash(phc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.String() != phc {
		t.Errorf("String() = %q, want %q", h.String(), phc)
	}
}

func TestNewPasswordHash_RejectsNonArgon2idScheme(t *testing.T) {
	if _, err := NewPasswordHash("$bcrypt$10$abcdefg"); err == nil {
		t.Fatal("expected error for non-argon2id scheme, got nil")
	}
}

func TestNewPasswordHash_RejectsEmpty(t *testing.T) {
	if _, err := NewPasswordHash(""); err == nil {
		t.Fatal("expected error for empty hash, got nil")
	}
}
