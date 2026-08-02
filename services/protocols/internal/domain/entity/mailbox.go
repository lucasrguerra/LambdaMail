package entity

import (
	"errors"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/domain/valueobject"
)

// Mailbox is a mail account entity (PLAN.md section 3).
type Mailbox struct {
	domainID  uuid.UUID
	address   valueobject.EmailAddress
	hash      valueobject.PasswordHash
	quota     valueobject.QuotaLimit
	usedBytes int64
}

var ErrMailboxQuotaExceeded = errors.New("mailbox: recording this usage would exceed the quota")

func NewMailbox(domainID uuid.UUID, address valueobject.EmailAddress, hash valueobject.PasswordHash, quota valueobject.QuotaLimit) *Mailbox {
	return &Mailbox{domainID: domainID, address: address, hash: hash, quota: quota}
}

func (m *Mailbox) Address() valueobject.EmailAddress { return m.address }
func (m *Mailbox) UsedBytes() int64                  { return m.usedBytes }
func (m *Mailbox) Quota() valueobject.QuotaLimit     { return m.quota }

// RecordUsage sets used_bytes, enforcing "used_bytes <= quota_bytes" (PLAN.md section 3).
func (m *Mailbox) RecordUsage(bytes int64) error {
	if m.quota.Exceeds(bytes) {
		return ErrMailboxQuotaExceeded
	}
	m.usedBytes = bytes
	return nil
}
