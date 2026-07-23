// Package entity contains the domain's aggregate roots and entities (Clean Architecture layer 1).
package entity

import (
	"errors"

	"lambdamail/protocols/internal/domain/valueobject"
)

// Domain is the aggregate root for a hosted mail domain (PLAN.md section 3).
type Domain struct {
	name               valueobject.DomainName
	active             bool
	activeMailboxCount int
}

var ErrDomainHasActiveMailboxes = errors.New("domain: cannot deactivate while it has active mailboxes")

func NewDomain(name valueobject.DomainName) *Domain {
	return &Domain{name: name, active: true}
}

func (d *Domain) Name() valueobject.DomainName { return d.name }
func (d *Domain) IsActive() bool               { return d.active }
func (d *Domain) ActiveMailboxCount() int      { return d.activeMailboxCount }

// RegisterMailboxCreated tracks a newly created active mailbox under this domain.
func (d *Domain) RegisterMailboxCreated() { d.activeMailboxCount++ }

// RegisterMailboxRemoved tracks a mailbox no longer counted as active under this domain.
func (d *Domain) RegisterMailboxRemoved() {
	if d.activeMailboxCount > 0 {
		d.activeMailboxCount--
	}
}

// Deactivate enforces the invariant "cannot deactivate with active mailboxes" (PLAN.md section 3).
func (d *Domain) Deactivate() error {
	if d.activeMailboxCount > 0 {
		return ErrDomainHasActiveMailboxes
	}
	d.active = false
	return nil
}
