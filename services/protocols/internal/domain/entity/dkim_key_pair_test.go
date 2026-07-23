package entity

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewDkimKeyPair_StartsPending(t *testing.T) {
	k := NewDkimKeyPair(uuid.New(), "sel1")
	if k.Status() != DkimStatusPending {
		t.Errorf("Status() = %v, want DkimStatusPending", k.Status())
	}
}

func TestDkimKeyPair_Activate_SetsActive(t *testing.T) {
	k := NewDkimKeyPair(uuid.New(), "sel1")
	k.Activate()
	if k.Status() != DkimStatusActive {
		t.Errorf("Status() = %v, want DkimStatusActive", k.Status())
	}
}
