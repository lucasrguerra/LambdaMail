package valueobject

import "testing"

func TestNewDomainName_AcceptsValidFQDN(t *testing.T) {
	d, err := NewDomainName("Example.COM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.String() != "example.com" {
		t.Errorf("String() = %q, want lowercased %q", d.String(), "example.com")
	}
}

func TestNewDomainName_RejectsEmpty(t *testing.T) {
	if _, err := NewDomainName(""); err == nil {
		t.Fatal("expected error for empty domain name, got nil")
	}
}

func TestNewDomainName_RejectsLabelWithoutDot(t *testing.T) {
	if _, err := NewDomainName("localhost"); err == nil {
		t.Fatal("expected error for single-label name (not a FQDN), got nil")
	}
}

func TestNewDomainName_RejectsTrailingHyphenLabel(t *testing.T) {
	if _, err := NewDomainName("bad-.example.com"); err == nil {
		t.Fatal("expected error for label ending in hyphen, got nil")
	}
}

func TestDomainName_IsPunycode_WithPunycodeLabel(t *testing.T) {
	d, err := NewDomainName("xn--exmple-cua.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !d.IsPunycode() {
		t.Error("IsPunycode() = false, want true for punycode domain")
	}
}

func TestDomainName_IsPunycode_WithoutPunycodeLabel(t *testing.T) {
	d, err := NewDomainName("example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.IsPunycode() {
		t.Error("IsPunycode() = true, want false for plain ASCII domain")
	}
}
