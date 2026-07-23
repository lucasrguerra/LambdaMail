package valueobject

import "testing"

func TestNewDeliveryStatusCode_AcceptsSuccess(t *testing.T) {
	c, err := NewDeliveryStatusCode(2, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.String() != "2.0.0" {
		t.Errorf("String() = %q, want %q", c.String(), "2.0.0")
	}
}

func TestNewDeliveryStatusCode_AcceptsPermanentFailure(t *testing.T) {
	c, err := NewDeliveryStatusCode(5, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.String() != "5.1.1" {
		t.Errorf("String() = %q, want %q", c.String(), "5.1.1")
	}
}

func TestNewDeliveryStatusCode_RejectsInvalidClass(t *testing.T) {
	if _, err := NewDeliveryStatusCode(3, 0, 0); err == nil {
		t.Fatal("expected error: RFC 3463 only defines classes 2, 4 and 5, got nil")
	}
}
