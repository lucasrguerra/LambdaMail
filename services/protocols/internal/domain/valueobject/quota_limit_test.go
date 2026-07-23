package valueobject

import "testing"

func TestNewQuotaLimit_AcceptsPositiveBytes(t *testing.T) {
	q, err := NewQuotaLimit(1073741824)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Bytes() != 1073741824 {
		t.Errorf("Bytes() = %d, want %d", q.Bytes(), 1073741824)
	}
}

func TestNewQuotaLimit_RejectsZero(t *testing.T) {
	if _, err := NewQuotaLimit(0); err == nil {
		t.Fatal("expected error for zero quota, got nil")
	}
}

func TestNewQuotaLimit_RejectsNegative(t *testing.T) {
	if _, err := NewQuotaLimit(-1); err == nil {
		t.Fatal("expected error for negative quota, got nil")
	}
}

func TestQuotaLimit_Exceeds(t *testing.T) {
	q, _ := NewQuotaLimit(1000)
	if !q.Exceeds(1001) {
		t.Error("Exceeds(1001) = false, want true for a 1000-byte quota")
	}
	if q.Exceeds(1000) {
		t.Error("Exceeds(1000) = true, want false: used_bytes <= quota_bytes is the invariant (PLAN.md section 3)")
	}
}
