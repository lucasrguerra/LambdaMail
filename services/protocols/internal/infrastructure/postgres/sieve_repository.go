package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// SieveRepository implements port.SieveRepository against PostgreSQL sieve_scripts table.
type SieveRepository struct {
	pool *pgxpool.Pool
}

func NewSieveRepository(pool *pgxpool.Pool) *SieveRepository {
	return &SieveRepository{pool: pool}
}

func (r *SieveRepository) GetScript(ctx context.Context, mailboxID string, name string) (*port.SieveScriptRecord, error) {
	var rec port.SieveScriptRecord
	err := r.pool.QueryRow(ctx, `
		SELECT id, mailbox_id, name, script, is_active
		FROM sieve_scripts
		WHERE mailbox_id = $1 AND name = $2
	`, mailboxID, name).Scan(&rec.ID, &rec.MailboxID, &rec.Name, &rec.Script, &rec.IsActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get sieve script %s: %w", name, err)
	}
	return &rec, nil
}

func (r *SieveRepository) PutScript(ctx context.Context, mailboxID string, name string, script string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sieve_scripts (mailbox_id, name, script, is_active)
		VALUES ($1, $2, $3, false)
		ON CONFLICT (mailbox_id, name) DO UPDATE SET script = EXCLUDED.script
	`, mailboxID, name, script)
	if err != nil {
		return fmt.Errorf("put sieve script %s: %w", name, err)
	}
	return nil
}

func (r *SieveRepository) ListScripts(ctx context.Context, mailboxID string) ([]port.SieveScriptRecord, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, mailbox_id, name, script, is_active
		FROM sieve_scripts
		WHERE mailbox_id = $1
		ORDER BY name ASC
	`, mailboxID)
	if err != nil {
		return nil, fmt.Errorf("list sieve scripts: %w", err)
	}
	defer rows.Close()

	var out []port.SieveScriptRecord
	for rows.Next() {
		var rec port.SieveScriptRecord
		if err := rows.Scan(&rec.ID, &rec.MailboxID, &rec.Name, &rec.Script, &rec.IsActive); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *SieveRepository) SetActiveScript(ctx context.Context, mailboxID string, name string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE sieve_scripts SET is_active = false WHERE mailbox_id = $1
	`, mailboxID); err != nil {
		return fmt.Errorf("deactivate sieve scripts: %w", err)
	}

	if name != "" {
		tag, err := tx.Exec(ctx, `
			UPDATE sieve_scripts SET is_active = true WHERE mailbox_id = $1 AND name = $2
		`, mailboxID, name)
		if err != nil {
			return fmt.Errorf("activate sieve script %s: %w", name, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("script %s not found", name)
		}
	}

	return tx.Commit(ctx)
}

func (r *SieveRepository) DeleteScript(ctx context.Context, mailboxID string, name string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM sieve_scripts WHERE mailbox_id = $1 AND name = $2 AND is_active = false
	`, mailboxID, name)
	if err != nil {
		return fmt.Errorf("delete sieve script %s: %w", name, err)
	}
	return nil
}

func (r *SieveRepository) RenameScript(ctx context.Context, mailboxID string, oldName string, newName string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE sieve_scripts SET name = $3 WHERE mailbox_id = $1 AND name = $2
	`, mailboxID, oldName, newName)
	if err != nil {
		return fmt.Errorf("rename sieve script %s to %s: %w", oldName, newName, err)
	}
	return nil
}

// ActiveScriptFor returns the mailbox's active script, for the delivery path.
//
// Delivery needs only this one question answered, so it is a method of its own
// rather than the delivery path reaching for the whole script-management
// surface that ManageSieve uses.
//
// A mailbox with no active script returns empty rather than an error: having
// no rules is the ordinary case, not a failure.
func (r *SieveRepository) ActiveScriptFor(ctx context.Context, mailboxID uuid.UUID) (string, error) {
	var script string
	err := r.pool.QueryRow(ctx, `
		SELECT script FROM sieve_scripts
		 WHERE mailbox_id = $1 AND is_active
		 LIMIT 1
	`, mailboxID).Scan(&script)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the active script for %s: %w", mailboxID, err)
	}
	return script, nil
}
