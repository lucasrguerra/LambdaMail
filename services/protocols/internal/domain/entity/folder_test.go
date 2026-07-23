package entity

import (
	"testing"

	"github.com/google/uuid"
)

func TestFolder_NextUID_StartsAtOneAndIsMonotonic(t *testing.T) {
	f := NewFolder(uuid.New(), "INBOX")
	first := f.NextUID()
	second := f.NextUID()
	third := f.NextUID()
	if first != 1 {
		t.Errorf("first NextUID() = %d, want 1 (RFC 3501 section 2.3.1.1 uid_next starts at 1)", first)
	}
	if second <= first || third <= second {
		t.Errorf("NextUID() sequence not strictly increasing: %d, %d, %d", first, second, third)
	}
}
