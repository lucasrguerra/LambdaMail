package port

import (
	"context"

	"github.com/google/uuid"
)

// MailboxAuth is the subset of mailbox data needed to verify a login and,
// once authenticated, to apply the sender policy of PLAN.md section 5.2.
type MailboxAuth struct {
	ID           uuid.UUID
	PasswordHash string
	EmailAddress string
	DomainName   string
	// MaxRecipientsPerHour caps a compromised account's blast radius.
	MaxRecipientsPerHour int
}

// AuthRepository resolves a login address to its stored credential.
type AuthRepository interface {
	// FindByAddress returns nil, nil (not an error) when no active mailbox
	// with an active domain matches address.
	FindByAddress(ctx context.Context, address string) (*MailboxAuth, error)
}
