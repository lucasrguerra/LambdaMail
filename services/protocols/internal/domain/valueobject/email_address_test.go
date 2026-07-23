package valueobject

import "testing"

func TestNewEmailAddress_AcceptsValidAddress(t *testing.T) {
	addr, err := NewEmailAddress("User.Name+tag@Example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr.LocalPart() != "User.Name+tag" {
		t.Errorf("local part = %q, want %q", addr.LocalPart(), "User.Name+tag")
	}
	if addr.Domain() != "example.com" {
		t.Errorf("domain = %q, want lowercased %q", addr.Domain(), "example.com")
	}
	if addr.String() != "User.Name+tag@example.com" {
		t.Errorf("String() = %q, want %q", addr.String(), "User.Name+tag@example.com")
	}
}

func TestNewEmailAddress_RejectsMissingAtSign(t *testing.T) {
	if _, err := NewEmailAddress("not-an-email"); err == nil {
		t.Fatal("expected error for address without '@', got nil")
	}
}

func TestNewEmailAddress_RejectsEmptyLocalPart(t *testing.T) {
	if _, err := NewEmailAddress("@example.com"); err == nil {
		t.Fatal("expected error for empty local part, got nil")
	}
}

func TestNewEmailAddress_RejectsEmptyDomain(t *testing.T) {
	if _, err := NewEmailAddress("user@"); err == nil {
		t.Fatal("expected error for empty domain, got nil")
	}
}

func TestNewEmailAddress_RejectsOverlongLocalPart(t *testing.T) {
	longLocal := ""
	for i := 0; i < 65; i++ {
		longLocal += "a"
	}
	if _, err := NewEmailAddress(longLocal + "@example.com"); err == nil {
		t.Fatal("expected error for local part over 64 octets (RFC 5321 4.5.3.1.1), got nil")
	}
}
