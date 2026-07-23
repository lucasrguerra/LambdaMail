package entity

import (
	"testing"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/domain/valueobject"
)

func newTestMailbox(t *testing.T) *Mailbox {
	t.Helper()
	addr, _ := valueobject.NewEmailAddress("user@example.com")
	hash, _ := valueobject.NewPasswordHash("$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG")
	quota, _ := valueobject.NewQuotaLimit(1000)
	return NewMailbox(uuid.New(), addr, hash, quota)
}

func TestNewMailbox_StartsWithZeroUsage(t *testing.T) {
	m := newTestMailbox(t)
	if m.UsedBytes() != 0 {
		t.Errorf("UsedBytes() = %d, want 0", m.UsedBytes())
	}
}

func TestMailbox_RecordUsage_AllowsUpToQuota(t *testing.T) {
	m := newTestMailbox(t)
	if err := m.RecordUsage(1000); err != nil {
		t.Fatalf("unexpected error recording usage exactly at quota: %v", err)
	}
	if m.UsedBytes() != 1000 {
		t.Errorf("UsedBytes() = %d, want 1000", m.UsedBytes())
	}
}

func TestMailbox_RecordUsage_RejectsOverQuota(t *testing.T) {
	m := newTestMailbox(t)
	if err := m.RecordUsage(1001); err == nil {
		t.Fatal("expected error: used_bytes must never exceed quota_bytes (PLAN.md section 3), got nil")
	}
}
