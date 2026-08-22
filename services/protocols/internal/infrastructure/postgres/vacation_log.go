package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VacationLog records who has already been told a mailbox is unattended.
type VacationLog struct {
	pool *pgxpool.Pool
}

func NewVacationLog(pool *pgxpool.Pool) *VacationLog {
	return &VacationLog{pool: pool}
}

// LastRepliedAt returns when this sender was last answered.
//
// A sender who has never been answered comes back as the zero time rather than
// an error: never having replied is the ordinary case, not a failure.
func (l *VacationLog) LastRepliedAt(
	ctx context.Context, mailboxID uuid.UUID, to string,
) (time.Time, error) {
	var last time.Time
	err := l.pool.QueryRow(ctx, `
		SELECT last_sent_at FROM vacation_replies
		 WHERE mailbox_id = $1 AND recipient = $2
	`, mailboxID, to).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read the vacation log for %s: %w", to, err)
	}
	return last, nil
}

// RecordReply notes that this sender has been told, now.
func (l *VacationLog) RecordReply(ctx context.Context, mailboxID uuid.UUID, to string) error {
	_, err := l.pool.Exec(ctx, `
		INSERT INTO vacation_replies (mailbox_id, recipient, last_sent_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (mailbox_id, recipient)
		DO UPDATE SET last_sent_at = NOW()
	`, mailboxID, to)
	if err != nil {
		return fmt.Errorf("record the vacation reply to %s: %w", to, err)
	}
	return nil
}
