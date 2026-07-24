package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// MessageQueryRepository implements port.MessageQueryRepository against Postgres.
type MessageQueryRepository struct {
	pool *pgxpool.Pool
}

func NewMessageQueryRepository(pool *pgxpool.Pool) *MessageQueryRepository {
	return &MessageQueryRepository{pool: pool}
}

func (r *MessageQueryRepository) ListMessages(ctx context.Context, folderID string) ([]port.MessageRecord, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.uid, m.blob_id, m.size_bytes, m.received_at, m.modseq,
		       COALESCE(array_agg(f.flag) FILTER (WHERE f.flag IS NOT NULL), '{}')
		FROM email_messages m
		LEFT JOIN message_flags f ON f.message_id = m.id AND f.received_at = m.received_at
		WHERE m.folder_id = $1 AND m.expunged_at IS NULL
		GROUP BY m.uid, m.blob_id, m.size_bytes, m.received_at, m.modseq
		ORDER BY m.uid ASC
	`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []port.MessageRecord
	for rows.Next() {
		var rec port.MessageRecord
		if err := rows.Scan(&rec.UID, &rec.BlobID, &rec.SizeBytes, &rec.ReceivedAt, &rec.ModSeq, &rec.Flags); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
