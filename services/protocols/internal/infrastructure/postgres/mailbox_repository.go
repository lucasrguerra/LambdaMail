package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// MailboxRepository implements port.MailboxRepository against Postgres.
type MailboxRepository struct {
	pool *pgxpool.Pool
}

func NewMailboxRepository(pool *pgxpool.Pool) *MailboxRepository {
	return &MailboxRepository{pool: pool}
}

func (r *MailboxRepository) FindActiveByAddress(ctx context.Context, address string) (*port.MailboxRecord, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT m.id, d.max_message_bytes
		FROM mailboxes m
		JOIN domains d ON d.id = m.domain_id
		WHERE m.email_address = $1
		  AND m.is_active = true
		  AND d.is_active = true
	`, address)

	var rec port.MailboxRecord
	var id uuid.UUID
	if err := row.Scan(&id, &rec.MaxMessageBytes); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.ID = id
	return &rec, nil
}
