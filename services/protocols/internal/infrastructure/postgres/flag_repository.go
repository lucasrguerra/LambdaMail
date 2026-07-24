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

func (r *FlagRepository) SetFlags(ctx context.Context, folderID string, uid uint32, op port.FlagOp, flags []string, unchangedSince uint64) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var currentModSeq uint64
	if err := tx.QueryRow(ctx, `
		SELECT modseq FROM email_messages WHERE folder_id = $1 AND uid = $2 AND expunged_at IS NULL
	`, folderID, uid).Scan(&currentModSeq); err != nil {
		return false, fmt.Errorf("find message folder=%s uid=%d: %w", folderID, uid, err)
	}

	if unchangedSince > 0 && currentModSeq > unchangedSince {
		return false, nil
	}

	var newModSeq uint64
	if err := tx.QueryRow(ctx, `
		UPDATE folders SET highest_modseq = highest_modseq + 1 WHERE id = $1 RETURNING highest_modseq
	`, folderID).Scan(&newModSeq); err != nil {
		return false, fmt.Errorf("advance highest_modseq for folder %s: %w", folderID, err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE email_messages SET modseq = $1 WHERE folder_id = $2 AND uid = $3 AND expunged_at IS NULL
	`, newModSeq, folderID, uid); err != nil {
		return false, fmt.Errorf("update message modseq: %w", err)
	}

	switch op {
	case port.FlagOpAdd:
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				INSERT INTO message_flags (message_id, received_at, flag)
				SELECT id, received_at, $3 FROM email_messages WHERE folder_id = $1 AND uid = $2 AND expunged_at IS NULL
				ON CONFLICT DO NOTHING
			`, folderID, uid, flag); err != nil {
				return false, fmt.Errorf("add flag %q: %w", flag, err)
			}
		}
	case port.FlagOpDel:
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				DELETE FROM message_flags WHERE (message_id, received_at) IN (
					SELECT id, received_at FROM email_messages WHERE folder_id = $1 AND uid = $2 AND expunged_at IS NULL
				) AND flag = $3
			`, folderID, uid, flag); err != nil {
				return false, fmt.Errorf("delete flag %q: %w", flag, err)
			}
		}
	case port.FlagOpSet:
		if _, err := tx.Exec(ctx, `
			DELETE FROM message_flags WHERE (message_id, received_at) IN (
				SELECT id, received_at FROM email_messages WHERE folder_id = $1 AND uid = $2 AND expunged_at IS NULL
			)
		`, folderID, uid); err != nil {
			return false, fmt.Errorf("clear flags before set: %w", err)
		}
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				INSERT INTO message_flags (message_id, received_at, flag)
				SELECT id, received_at, $3 FROM email_messages WHERE folder_id = $1 AND uid = $2 AND expunged_at IS NULL
				ON CONFLICT DO NOTHING
			`, folderID, uid, flag); err != nil {
				return false, fmt.Errorf("set flag %q: %w", flag, err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}
