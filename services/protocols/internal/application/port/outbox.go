package port

import (
	"context"

	"github.com/google/uuid"
)

// OutboxEvent is one row of domain_events_outbox.
type OutboxEvent struct {
	ID          int64
	EventType   string
	AggregateID uuid.UUID
	Payload     []byte
}

// OutboxRepository reads and retires the transactional outbox.
type OutboxRepository interface {
	// FetchUnpublished returns the oldest unpublished events, oldest first, so
	// a subscriber sees them in the order they happened.
	FetchUnpublished(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkPublished(ctx context.Context, ids []int64) error
}

// EventPublisher fans a domain event out to whoever is listening. It must not
// block: the relay holds no lock, but a slow subscriber would still stall the
// batch behind it.
type EventPublisher interface {
	Publish(event OutboxEvent)
}
