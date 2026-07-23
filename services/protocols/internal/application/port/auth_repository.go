package port

import (
	"context"

	"github.com/google/uuid"
)

// MailboxAuth is the subset of mailbox data needed to verify a login.
type MailboxAuth struct {
	ID           uuid.UUID
	PasswordHash string
}

// AuthRepository resolves a login address to its stored credential.
type AuthRepository interface {
	// FindByAddress returns nil, nil (not an error) when no active mailbox
	// with an active domain matches address.
	FindByAddress(ctx context.Context, address string) (*MailboxAuth, error)
}
