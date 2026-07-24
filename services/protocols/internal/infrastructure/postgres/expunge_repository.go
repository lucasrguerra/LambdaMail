package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExpungeRepository implements port.ExpungeRepository against Postgres.
type ExpungeRepository struct {
	pool *pgxpool.Pool
}

func NewExpungeRepository(pool *pgxpool.Pool) *ExpungeRepository {
	return &ExpungeRepository{pool: pool}
}

// Expunge marks each message identified by (folderID, uid) in uids as
// expunged, decrements the folder's total_count (and unread_count for any
// message that lacked \Seen), and decrements the owning mailbox's
// used_bytes by the message's size_bytes. used_bytes is logical,
// message-level accounting (mirroring how delivery and COPY add to it per
// message even when the underlying blob is deduplicated), so it is
// decremented here independently of message_blobs.ref_count, whose
// reclamation is deferred to a future GC task. One transaction per call;
// each uid is processed sequentially within it.
func (r *ExpungeRepository) Expunge(ctx context.Context, folderID string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, uid := range uids {
		var wasSeen bool
		var mailboxID string
		var sizeBytes int64
		err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM message_flags mf
				JOIN email_messages m ON m.id = mf.message_id AND m.received_at = mf.received_at
				WHERE m.folder_id = $1 AND m.uid = $2 AND mf.flag = '\Seen'
			), m.mailbox_id, m.size_bytes
			FROM email_messages m
			WHERE m.folder_id = $1 AND m.uid = $2
		`, folderID, uid).Scan(&wasSeen, &mailboxID, &sizeBytes)
		if err != nil {
			return fmt.Errorf("check seen flag for uid %d: %w", uid, err)
		}

		tag, err := tx.Exec(ctx, `
			UPDATE email_messages SET expunged_at = NOW()
			WHERE folder_id = $1 AND uid = $2 AND expunged_at IS NULL
		`, folderID, uid)
		if err != nil {
			return fmt.Errorf("expunge uid %d: %w", uid, err)
		}
		if tag.RowsAffected() == 0 {
			// Nothing to expunge (already expunged or never existed) - the
			// caller already verified \Deleted, so don't touch counters for
			// a message that wasn't actually there.
			continue
		}

		unreadDelta := 0
		if !wasSeen {
			unreadDelta = 1
		}
		if _, err := tx.Exec(ctx, `
			UPDATE folders SET total_count = total_count - 1, unread_count = unread_count - $1
			WHERE id = $2
		`, unreadDelta, folderID); err != nil {
			return fmt.Errorf("decrement folder counters for uid %d: %w", uid, err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE mailboxes SET used_bytes = used_bytes - $1
			WHERE id = $2
		`, sizeBytes, mailboxID); err != nil {
			return fmt.Errorf("decrement mailbox used_bytes for uid %d: %w", uid, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
