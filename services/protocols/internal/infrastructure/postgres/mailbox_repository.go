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
		SELECT m.id, d.max_message_bytes, m.quota_bytes, m.used_bytes
		FROM mailboxes m
		JOIN domains d ON d.id = m.domain_id
		WHERE m.email_address = $1
		  AND m.is_active = true
		  AND d.is_active = true
	`, address)

	var rec port.MailboxRecord
	var id uuid.UUID
	if err := row.Scan(&id, &rec.MaxMessageBytes, &rec.QuotaBytes, &rec.UsedBytes); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.ID = id
	return &rec, nil
}

// ResolveDeliveryTargets tries a direct mailbox match first, then falls back
// to aliases: an exact source_address match takes precedence over a
// domain-wide catch-all. Destination addresses that don't resolve to an
// internal active mailbox (e.g. external forwarding) are silently skipped -
// outbound delivery doesn't exist yet in this sub-project's scope.
func (r *MailboxRepository) ResolveDeliveryTargets(ctx context.Context, address string) ([]port.MailboxRecord, error) {
	direct, err := r.FindActiveByAddress(ctx, address)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return []port.MailboxRecord{*direct}, nil
	}

	var destinations []string
	err = r.pool.QueryRow(ctx, `
		SELECT a.destination_addresses
		FROM aliases a
		JOIN domains d ON d.id = a.domain_id
		WHERE d.is_active = true
		  AND a.is_active = true
		  AND d.name = split_part($1, '@', 2)
		  AND (a.source_address = $1 OR (a.is_catch_all AND a.source_address = '*@' || split_part($1, '@', 2)))
		ORDER BY a.is_catch_all ASC
		LIMIT 1
	`, address).Scan(&destinations)
	if err != nil {
		if err == pgx.ErrNoRows {
			return []port.MailboxRecord{}, nil
		}
		return nil, err
	}

	targets := []port.MailboxRecord{}
	for _, dest := range destinations {
		rec, err := r.FindActiveByAddress(ctx, dest)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			targets = append(targets, *rec)
		}
	}
	return targets, nil
}
