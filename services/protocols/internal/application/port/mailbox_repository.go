// Package port declares the interfaces the application layer depends on.
// Zero infrastructure imports: implementations live in internal/infrastructure.
package port

import (
	"context"

	"github.com/google/uuid"
)

// MailboxRecord is the subset of mailbox + domain data the inbound flow needs.
type MailboxRecord struct {
	ID              uuid.UUID
	MaxMessageBytes int64
}

// MailboxRepository resolves a recipient address to an active mailbox.
type MailboxRepository interface {
	// FindActiveByAddress returns nil, nil (not an error) when no active
	// mailbox with an active domain matches address.
	FindActiveByAddress(ctx context.Context, address string) (*MailboxRecord, error)
}
