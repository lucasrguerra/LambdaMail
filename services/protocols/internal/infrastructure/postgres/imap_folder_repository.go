package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// ImapFolderRepository implements port.ImapFolderRepository against Postgres.
type ImapFolderRepository struct {
	pool *pgxpool.Pool
}

func NewImapFolderRepository(pool *pgxpool.Pool) *ImapFolderRepository {
	return &ImapFolderRepository{pool: pool}
}

func (r *ImapFolderRepository) FindByName(ctx context.Context, mailboxID, name string) (*port.ImapFolderRecord, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT f.id, f.name, f.uid_next, f.uid_validity, f.total_count
		FROM folders f
		WHERE f.mailbox_id = $1 AND f.name = $2
	`, mailboxID, name)

	var rec port.ImapFolderRecord
	if err := row.Scan(&rec.ID, &rec.Name, &rec.UIDNext, &rec.UIDValidity, &rec.NumMessages); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (r *ImapFolderRepository) ListFolders(ctx context.Context, mailboxID string) ([]port.ImapFolderRecord, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT f.id, f.name, f.uid_next, f.uid_validity, f.total_count
		FROM folders f
		WHERE f.mailbox_id = $1
		ORDER BY f.name
	`, mailboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []port.ImapFolderRecord
	for rows.Next() {
		var rec port.ImapFolderRecord
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.UIDNext, &rec.UIDValidity, &rec.NumMessages); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
