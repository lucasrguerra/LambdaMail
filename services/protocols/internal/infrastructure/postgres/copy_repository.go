package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// CopyRepository implements port.CopyRepository against Postgres. One
// transaction per copied message, mirroring InboundMessageRepository's
// established per-message transactional pattern (PLAN.md section 6.1).
type CopyRepository struct {
	pool *pgxpool.Pool
}

func NewCopyRepository(pool *pgxpool.Pool) *CopyRepository {
	return &CopyRepository{pool: pool}
}

func (r *CopyRepository) CopyMessages(ctx context.Context, sourceFolderID string, uids []uint32, destFolderID string) ([]port.CopiedMessage, error) {
	results := make([]port.CopiedMessage, 0, len(uids))
	for _, uid := range uids {
		destUID, err := r.copyOne(ctx, sourceFolderID, uid, destFolderID)
		if err != nil {
			return nil, fmt.Errorf("copy uid %d: %w", uid, err)
		}
		results = append(results, port.CopiedMessage{SourceUID: uid, DestUID: destUID})
	}
	return results, nil
}

func (r *CopyRepository) copyOne(ctx context.Context, sourceFolderID string, sourceUID uint32, destFolderID string) (uint32, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var sourceMessageID string
	var blobID, senderAddress string
	var recipientAddresses []string
	var sizeBytes int64
	var spfResult, dkimResult, dmarcResult *string
	var fromDisplayName, subject, snippet, messageIDHeader, inReplyTo *string
	var threadID *uuid.UUID
	var hasAttachments bool
	var arcResult, daneStatus, tlsVersion, spamVerdict, virusName *string
	var spamScore float64
	err = tx.QueryRow(ctx, `
		SELECT id, blob_id, sender_address, recipient_addresses, size_bytes, spf_result, dkim_result, dmarc_result,
			from_display_name, subject, snippet, message_id_header, in_reply_to, thread_id, has_attachments,
			arc_result, dane_status, tls_version, spam_score, spam_verdict, virus_name
		FROM email_messages WHERE folder_id = $1 AND uid = $2 AND expunged_at IS NULL
	`, sourceFolderID, sourceUID).Scan(&sourceMessageID, &blobID, &senderAddress, &recipientAddresses, &sizeBytes, &spfResult, &dkimResult, &dmarcResult,
		&fromDisplayName, &subject, &snippet, &messageIDHeader, &inReplyTo, &threadID, &hasAttachments,
		&arcResult, &daneStatus, &tlsVersion, &spamScore, &spamVerdict, &virusName)
	if err != nil {
		return 0, fmt.Errorf("find source message: %w", err)
	}

	// uid_next is BIGINT; the destination UID we allocate for this message
	// is the folder's current uid_next value before advancing it.
	var destUIDNext int64
	if err := tx.QueryRow(ctx, `
		SELECT uid_next FROM folders WHERE id = $1 FOR UPDATE
	`, destFolderID).Scan(&destUIDNext); err != nil {
		return 0, fmt.Errorf("lock destination folder: %w", err)
	}
	destUID := uint32(destUIDNext)

	if _, err := tx.Exec(ctx, `UPDATE folders SET uid_next = uid_next + 1, total_count = total_count + 1 WHERE id = $1`, destFolderID); err != nil {
		return 0, fmt.Errorf("advance destination uid_next: %w", err)
	}

	var wasSeen bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM message_flags WHERE message_id = $1 AND flag = '\Seen')
	`, sourceMessageID).Scan(&wasSeen); err != nil {
		return 0, fmt.Errorf("check source seen flag: %w", err)
	}
	if !wasSeen {
		if _, err := tx.Exec(ctx, `UPDATE folders SET unread_count = unread_count + 1 WHERE id = $1`, destFolderID); err != nil {
			return 0, fmt.Errorf("increment destination unread_count: %w", err)
		}
	}

	var destMailboxID string
	if err := tx.QueryRow(ctx, `SELECT mailbox_id FROM folders WHERE id = $1`, destFolderID).Scan(&destMailboxID); err != nil {
		return 0, fmt.Errorf("find destination mailbox: %w", err)
	}

	var destMessageID string
	var destReceivedAt any
	err = tx.QueryRow(ctx, `
		INSERT INTO email_messages (
			mailbox_id, folder_id, uid, blob_id,
			sender_address, recipient_addresses, size_bytes,
			spf_result, dkim_result, dmarc_result,
			from_display_name, subject, snippet, message_id_header, in_reply_to, thread_id, has_attachments,
			arc_result, dane_status, tls_version, spam_score, spam_verdict, virus_name
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		RETURNING id, received_at
	`, destMailboxID, destFolderID, destUID, blobID, senderAddress, recipientAddresses, sizeBytes, spfResult, dkimResult, dmarcResult,
		fromDisplayName, subject, snippet, messageIDHeader, inReplyTo, threadID, hasAttachments,
		arcResult, daneStatus, tlsVersion, spamScore, spamVerdict, virusName).Scan(&destMessageID, &destReceivedAt)
	if err != nil {
		return 0, fmt.Errorf("insert copied message: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE message_blobs SET ref_count = ref_count + 1 WHERE id = $1`, blobID); err != nil {
		return 0, fmt.Errorf("increment blob ref_count: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE mailboxes SET used_bytes = used_bytes + $1 WHERE id = $2`, sizeBytes, destMailboxID); err != nil {
		return 0, fmt.Errorf("increment destination mailbox used_bytes: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO message_flags (message_id, received_at, flag)
		SELECT $1, $2, flag FROM message_flags WHERE message_id = $3
	`, destMessageID, destReceivedAt, sourceMessageID); err != nil {
		return 0, fmt.Errorf("copy flags: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return destUID, nil
}
