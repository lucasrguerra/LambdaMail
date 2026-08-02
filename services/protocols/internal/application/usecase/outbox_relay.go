package usecase

import (
	"context"
	"log"
	"time"

	"lambdamail/protocols/internal/application/port"
)

// OutboxRelay publishes domain events recorded inside the delivery transaction
// (PLAN.md section 9.4).
//
// Events are read from the table rather than emitted in-process because that
// is the whole point of the outbox: the row is written in the same transaction
// as the message, so an event can never describe a delivery that rolled back,
// and a crash between commit and publish loses nothing - the row is still
// there on restart.
type OutboxRelay struct {
	repo      port.OutboxRepository
	publisher port.EventPublisher
	batchSize int
	interval  time.Duration
}

func NewOutboxRelay(repo port.OutboxRepository, publisher port.EventPublisher) *OutboxRelay {
	return &OutboxRelay{repo: repo, publisher: publisher, batchSize: 100, interval: time.Second}
}

// Run polls until the context is cancelled.
func (r *OutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.PublishBatch(ctx); err != nil {
				log.Printf("outbox relay: %v", err)
			}
		}
	}
}

// PublishBatch drains one batch and reports how many events were published.
func (r *OutboxRelay) PublishBatch(ctx context.Context) (int, error) {
	events, err := r.repo.FetchUnpublished(ctx, r.batchSize)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}

	ids := make([]int64, 0, len(events))
	for _, event := range events {
		// Delivery is best effort: a subscriber that is not connected simply
		// misses the push and reconciles on its next reconnect. Holding the
		// row back for an absent listener would stall every other event.
		r.publisher.Publish(event)
		ids = append(ids, event.ID)
	}

	if err := r.repo.MarkPublished(ctx, ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}
