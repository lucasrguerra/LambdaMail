package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// OutboxRepository implements the transactional outbox against Postgres.
type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

func (r *OutboxRepository) FetchUnpublished(ctx context.Context, limit int) ([]port.OutboxEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	// SKIP LOCKED so two instances of this service can both relay without
	// either publishing the same event twice or waiting on the other.
	rows, err := r.pool.Query(ctx, `
		SELECT id, event_type, aggregate_id, payload
		  FROM domain_events_outbox
		 WHERE published_at IS NULL
		 ORDER BY id
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch outbox: %w", err)
	}
	defer rows.Close()

	events := []port.OutboxEvent{}
	for rows.Next() {
		var e port.OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.AggregateID, &e.Payload); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (r *OutboxRepository) MarkPublished(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE domain_events_outbox SET published_at = NOW() WHERE id = ANY($1)`, ids)
	return err
}
