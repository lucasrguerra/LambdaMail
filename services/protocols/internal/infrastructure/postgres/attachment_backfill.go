package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AttachmentBackfill repairs has_attachments on messages already stored.
//
// The flag was only ever set for multipart messages, so a single-part
// attachment - a DMARC aggregate report, whose entire body is a zip - was
// filed as having none, and showed up without a paperclip beside TLS-RPT
// reports from the same sender that happened to carry a text part.
type AttachmentBackfill struct {
	pool *pgxpool.Pool
}

func NewAttachmentBackfill(pool *pgxpool.Pool) *AttachmentBackfill {
	return &AttachmentBackfill{pool: pool}
}

// Done reports whether this repair has already run.
func (b *AttachmentBackfill) Done(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := b.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM backfills WHERE name = $1)`, name).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("read backfill marker %s: %w", name, err)
	}
	return exists, nil
}

func (b *AttachmentBackfill) MarkDone(ctx context.Context, name, detail string) error {
	_, err := b.pool.Exec(ctx,
		`INSERT INTO backfills (name, detail) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`,
		name, detail)
	if err != nil {
		return fmt.Errorf("record backfill marker %s: %w", name, err)
	}
	return nil
}

// Candidate is one message worth re-reading.
type Candidate struct {
	ID     uuid.UUID
	BlobID uuid.UUID
}

// Candidates lists messages currently flagged as having no attachment.
//
// Only those: the old rule never claimed an attachment that was not there, so
// a true flag needs no second opinion and the scan stays as small as the bug.
func (b *AttachmentBackfill) Candidates(ctx context.Context, limit int) ([]Candidate, error) {
	rows, err := b.pool.Query(ctx, `
		SELECT id, blob_id FROM email_messages
		 WHERE has_attachments = false AND expunged_at IS NULL
		 ORDER BY received_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list attachment backfill candidates: %w", err)
	}
	defer rows.Close()

	out := []Candidate{}
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.ID, &c.BlobID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetHasAttachments records the recomputed flag.
func (b *AttachmentBackfill) SetHasAttachments(ctx context.Context, messageID uuid.UUID) error {
	_, err := b.pool.Exec(ctx,
		`UPDATE email_messages SET has_attachments = true WHERE id = $1`, messageID)
	if err != nil {
		return fmt.Errorf("set has_attachments for %s: %w", messageID, err)
	}
	return nil
}
