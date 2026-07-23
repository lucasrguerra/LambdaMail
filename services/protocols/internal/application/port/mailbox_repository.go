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
	QuotaBytes      int64
	UsedBytes       int64
}

// MailboxRepository resolves a recipient address to the active mailbox(es)
// mail for it should be delivered to.
type MailboxRepository interface {
	// FindActiveByAddress returns nil, nil (not an error) when no active
	// mailbox with an active domain matches address.
	FindActiveByAddress(ctx context.Context, address string) (*MailboxRecord, error)

	// ResolveDeliveryTargets returns every active mailbox mail for address
	// should be delivered to: the mailbox itself when address matches one
	// directly, or the mailbox(es) reached through a matching alias
	// (source_address exact match takes precedence over a domain
	// catch-all). Returns an empty, non-nil slice (not an error) when
	// nothing matches - a direct mailbox miss, an alias with no active
	// destination, or no alias at all are all "not found", not failures.
	ResolveDeliveryTargets(ctx context.Context, address string) ([]MailboxRecord, error)
}
