package entity

import "github.com/google/uuid"

// DkimStatus is the lifecycle state of a DkimKeyPair (PLAN.md section 9, dkim_keys.status).
type DkimStatus int

const (
	DkimStatusPending DkimStatus = iota
	DkimStatusActive
	DkimStatusRetiring
	DkimStatusRevoked
)

// DkimKeyPair is a DKIM signing key with a selector (PLAN.md section 3).
// Invariant enforced at the aggregate boundary (Domain), not here: only one
// ACTIVE key per (domain, algorithm) - see the unique partial index in migrations/0001.
type DkimKeyPair struct {
	domainID uuid.UUID
	selector string
	status   DkimStatus
}

func NewDkimKeyPair(domainID uuid.UUID, selector string) *DkimKeyPair {
	return &DkimKeyPair{domainID: domainID, selector: selector, status: DkimStatusPending}
}

func (k *DkimKeyPair) Selector() string   { return k.selector }
func (k *DkimKeyPair) Status() DkimStatus { return k.status }
func (k *DkimKeyPair) Activate()          { k.status = DkimStatusActive }
func (k *DkimKeyPair) Retire()            { k.status = DkimStatusRetiring }
func (k *DkimKeyPair) Revoke()            { k.status = DkimStatusRevoked }
