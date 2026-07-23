package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// FlagRepository implements port.FlagRepository against Postgres.
type FlagRepository struct {
	pool *pgxpool.Pool
}

func NewFlagRepository(pool *pgxpool.Pool) *FlagRepository {
	return &FlagRepository{pool: pool}
}

func (r *FlagRepository) SetFlags(ctx context.Context, folderID string, uid uint32, op port.FlagOp, flags []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var messageID string
	var receivedAt any
	if err := tx.QueryRow(ctx, `
		SELECT id, received_at FROM email_messages WHERE folder_id = $1 AND uid = $2
	`, folderID, uid).Scan(&messageID, &receivedAt); err != nil {
		return fmt.Errorf("find message folder=%s uid=%d: %w", folderID, uid, err)
	}

	switch op {
	case port.FlagOpAdd:
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				INSERT INTO message_flags (message_id, received_at, flag) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING
			`, messageID, receivedAt, flag); err != nil {
				return fmt.Errorf("add flag %q: %w", flag, err)
			}
		}
	case port.FlagOpDel:
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				DELETE FROM message_flags WHERE message_id = $1 AND received_at = $2 AND flag = $3
			`, messageID, receivedAt, flag); err != nil {
				return fmt.Errorf("delete flag %q: %w", flag, err)
			}
		}
	case port.FlagOpSet:
		if _, err := tx.Exec(ctx, `DELETE FROM message_flags WHERE message_id = $1 AND received_at = $2`, messageID, receivedAt); err != nil {
			return fmt.Errorf("clear flags before set: %w", err)
		}
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				INSERT INTO message_flags (message_id, received_at, flag) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING
			`, messageID, receivedAt, flag); err != nil {
				return fmt.Errorf("set flag %q: %w", flag, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
