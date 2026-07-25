package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// InboundMessageRepository implements port.InboundMessageRepository against
// Postgres: one transaction per Persist call, matching PLAN.md section 6.1's
// "250 OK only after fsync + COMMIT" rule (the fsync happens in BlobStorage;
// this repository's COMMIT is the second half of that durability guarantee).
type InboundMessageRepository struct {
	pool *pgxpool.Pool
}

func NewInboundMessageRepository(pool *pgxpool.Pool) *InboundMessageRepository {
	return &InboundMessageRepository{pool: pool}
}

func (r *InboundMessageRepository) Persist(ctx context.Context, input port.PersistInboundMessageInput) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) // no-op if Commit succeeds

	targetFolder := input.TargetFolderName
	if targetFolder == "" {
		targetFolder = "INBOX"
	}

	var folderID string
	var uid int64
	err = tx.QueryRow(ctx, `
		SELECT id, uid_next FROM folders
		WHERE mailbox_id = $1 AND (special_use = LOWER($2) OR LOWER(name) = LOWER($2))
		ORDER BY special_use IS NOT NULL DESC
		LIMIT 1
		FOR UPDATE
	`, input.MailboxID, targetFolder).Scan(&folderID, &uid)
	if err != nil {
		return 0, fmt.Errorf("find folder %q for mailbox %s: %w", targetFolder, input.MailboxID, err)
	}

	var modseq int64
	if err := tx.QueryRow(ctx, `
		UPDATE folders SET uid_next = uid_next + 1, highest_modseq = highest_modseq + 1, unread_count = unread_count + 1, total_count = total_count + 1
		WHERE id = $1
		RETURNING highest_modseq
	`, folderID).Scan(&modseq); err != nil {
		return 0, fmt.Errorf("advance uid_next, highest_modseq and folder counters: %w", err)
	}

	// email_messages.id defaults to gen_random_uuid() per the migration - no explicit value needed.
	_, err = tx.Exec(ctx, `
		INSERT INTO email_messages (
			mailbox_id, folder_id, uid, modseq, blob_id,
			sender_address, recipient_addresses, size_bytes,
			spf_result, dkim_result, dmarc_result
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, input.MailboxID, folderID, uid, modseq, input.Blob.ID,
		input.SenderAddress, []string{input.RecipientAddress}, input.Blob.SizeBytes,
		input.SPFResult, input.DKIMResult, input.DMARCResult)
	if err != nil {
		return 0, fmt.Errorf("insert email_messages: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE message_blobs SET ref_count = ref_count + 1 WHERE id = $1`, input.Blob.ID); err != nil {
		return 0, fmt.Errorf("increment blob ref_count: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE mailboxes SET used_bytes = used_bytes + $1 WHERE id = $2`, input.Blob.SizeBytes, input.MailboxID); err != nil {
		return 0, fmt.Errorf("increment mailbox used_bytes: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO domain_events_outbox (event_type, aggregate_id, payload)
		VALUES ('EmailReceived', $1, $2)
	`, input.MailboxID, fmt.Sprintf(`{"recipient":%q,"uid":%d}`, input.RecipientAddress, uid)); err != nil {
		return 0, fmt.Errorf("insert outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return uid, nil
}
