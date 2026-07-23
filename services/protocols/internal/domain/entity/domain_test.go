package entity

import (
	"testing"

	"lambdamail/protocols/internal/domain/valueobject"
)

func TestNewDomain_StartsActiveWithNoMailboxes(t *testing.T) {
	name, _ := valueobject.NewDomainName("example.com")
	d := NewDomain(name)
	if !d.IsActive() {
		t.Error("IsActive() = false, want true for a newly created domain")
	}
	if d.ActiveMailboxCount() != 0 {
		t.Errorf("ActiveMailboxCount() = %d, want 0", d.ActiveMailboxCount())
	}
}

func TestDomain_Deactivate_SucceedsWithNoActiveMailboxes(t *testing.T) {
	name, _ := valueobject.NewDomainName("example.com")
	d := NewDomain(name)
	if err := d.Deactivate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.IsActive() {
		t.Error("IsActive() = true after Deactivate(), want false")
	}
}

func TestDomain_Deactivate_FailsWithActiveMailboxes(t *testing.T) {
	name, _ := valueobject.NewDomainName("example.com")
	d := NewDomain(name)
	d.RegisterMailboxCreated()
	if err := d.Deactivate(); err == nil {
		t.Fatal("expected error: a domain with active mailboxes must not be deactivatable (PLAN.md section 3)")
	}
}
