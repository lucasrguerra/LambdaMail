package valueobject

import "testing"

func TestNewTLSARecord_AcceptsUsage3Selector1MatchingType1(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	r, err := NewTLSARecord(3, 1, 1, hash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Usage() != 3 || r.Selector() != 1 || r.MatchingType() != 1 {
		t.Errorf("fields not preserved: got usage=%d selector=%d matchingType=%d", r.Usage(), r.Selector(), r.MatchingType())
	}
}

func TestNewTLSARecord_RejectsUsage2(t *testing.T) {
	// DANE_TLSA_USAGE=2 was explicitly rejected by ADR (PLAN.md section 5.1):
	// Let's Encrypt rotates intermediates without notice, making usage 2 unpredictable.
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := NewTLSARecord(2, 1, 1, hash); err == nil {
		t.Fatal("expected error for usage=2 (PKIX-CA), got nil - rejected by ADR in PLAN.md section 5.1")
	}
}

func TestNewTLSARecord_RejectsWrongLengthHash(t *testing.T) {
	if _, err := NewTLSARecord(3, 1, 1, "tooshort"); err == nil {
		t.Fatal("expected error for a SHA-256 hash that isn't 64 hex chars, got nil")
	}
}
