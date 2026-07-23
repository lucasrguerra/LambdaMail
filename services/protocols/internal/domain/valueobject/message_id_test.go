package valueobject

import "testing"

func TestNewMessageID_AcceptsAngleBracketForm(t *testing.T) {
	id, err := NewMessageID("<abc123@mail.example.com>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id.String() != "<abc123@mail.example.com>" {
		t.Errorf("String() = %q, want input preserved", id.String())
	}
}

func TestNewMessageID_RejectsEmpty(t *testing.T) {
	if _, err := NewMessageID(""); err == nil {
		t.Fatal("expected error for empty Message-ID, got nil")
	}
}

func TestNewMessageID_RejectsMissingAngleBrackets(t *testing.T) {
	if _, err := NewMessageID("abc123@mail.example.com"); err == nil {
		t.Fatal("expected error for Message-ID without angle brackets, got nil")
	}
}
