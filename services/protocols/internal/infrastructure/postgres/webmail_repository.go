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

// ErrMessageNotFound is returned when a message does not exist, or exists but
// belongs to another mailbox. The two are deliberately indistinguishable:
// telling them apart would let a caller probe for other people's UIDs.
var ErrMessageNotFound = errors.New("webmail: message not found")

// WebmailRepository implements the webmail read side against Postgres.
type WebmailRepository struct {
	pool *pgxpool.Pool
}

func NewWebmailRepository(pool *pgxpool.Pool) *WebmailRepository {
	return &WebmailRepository{pool: pool}
}

func (r *WebmailRepository) FindMailboxIDByAddress(ctx context.Context, address string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		SELECT m.id::text FROM mailboxes m
		  JOIN domains d ON d.id = m.domain_id
		 WHERE m.email_address = $1 AND m.is_active AND d.is_active
	`, address).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrMessageNotFound
	}
	return id, err
}

func (r *WebmailRepository) ListFolders(ctx context.Context, mailboxID string) ([]port.WebmailFolder, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, name, COALESCE(special_use, ''), unread_count, total_count
		  FROM folders WHERE mailbox_id = $1
		 ORDER BY CASE special_use
		            WHEN 'inbox' THEN 0 WHEN 'drafts' THEN 1 WHEN 'sent' THEN 2
		            WHEN 'archive' THEN 3 WHEN 'junk' THEN 4 WHEN 'trash' THEN 5
		            ELSE 6 END, name
	`, mailboxID)
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	defer rows.Close()

	folders := []port.WebmailFolder{}
	for rows.Next() {
		var f port.WebmailFolder
		if err := rows.Scan(&f.ID, &f.Name, &f.SpecialUse, &f.UnreadCount, &f.TotalCount); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

// folderCondition matches either the special-use role ("inbox") or the literal
// folder name, so the UI can address a folder the same way IMAP does.
const folderCondition = `(f.special_use = LOWER($2) OR LOWER(f.name) = LOWER($2))`

func (r *WebmailRepository) ListMessages(
	ctx context.Context, mailboxID, folderName, search string, limit, offset int,
) ([]port.WebmailMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := r.pool.Query(ctx, `
		SELECT e.uid, COALESCE(e.subject, ''), e.sender_address::text,
		       COALESCE(e.from_display_name, ''), COALESCE(e.snippet, ''),
		       e.size_bytes, e.received_at, e.has_attachments,
		       EXISTS (SELECT 1 FROM message_flags mf
		                WHERE mf.message_id = e.id AND mf.received_at = e.received_at AND mf.flag = '\Seen'),
		       EXISTS (SELECT 1 FROM message_flags mf
		                WHERE mf.message_id = e.id AND mf.received_at = e.received_at AND mf.flag = '\Flagged'),
		       EXISTS (SELECT 1 FROM message_flags mf
		                WHERE mf.message_id = e.id AND mf.received_at = e.received_at AND mf.flag = '\Answered'),
		       COALESCE(e.spam_verdict, ''), COALESCE(e.dmarc_result, '')
		  FROM email_messages e
		  JOIN folders f ON f.id = e.folder_id
		 WHERE e.mailbox_id = $1 AND `+folderCondition+`
		   AND e.expunged_at IS NULL
		   AND ($3 = '' OR e.subject ILIKE '%%' || $3 || '%%'
		                OR e.sender_address::text ILIKE '%%' || $3 || '%%'
		                OR e.snippet ILIKE '%%' || $3 || '%%')
		 ORDER BY e.received_at DESC, e.uid DESC
		 LIMIT $4 OFFSET $5
	`, mailboxID, folderName, search, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	messages := []port.WebmailMessage{}
	for rows.Next() {
		var m port.WebmailMessage
		if err := rows.Scan(
			&m.UID, &m.Subject, &m.SenderAddress, &m.FromName, &m.Snippet,
			&m.SizeBytes, &m.ReceivedAt, &m.HasAttachments,
			&m.Seen, &m.Flagged, &m.Answered, &m.SpamVerdict, &m.DmarcResult,
		); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

func (r *WebmailRepository) GetMessageBlob(
	ctx context.Context, mailboxID, folderName string, uid uint32,
) (uuid.UUID, error) {
	var blobID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT e.blob_id FROM email_messages e
		  JOIN folders f ON f.id = e.folder_id
		 WHERE e.mailbox_id = $1 AND `+folderCondition+`
		   AND e.uid = $3 AND e.expunged_at IS NULL
	`, mailboxID, folderName, int64(uid)).Scan(&blobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrMessageNotFound
	}
	return blobID, err
}

// MarkSeen keeps the \Seen flag and the folder's unread counter in step. They
// are updated together in one transaction, because a counter that disagrees
// with the flags is what makes an inbox show unread mail that is not there.
func (r *WebmailRepository) MarkSeen(
	ctx context.Context, mailboxID, folderName string, uid uint32, seen bool,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var messageID uuid.UUID
	var receivedAt any
	var folderID string
	err = tx.QueryRow(ctx, `
		SELECT e.id, e.received_at, f.id::text FROM email_messages e
		  JOIN folders f ON f.id = e.folder_id
		 WHERE e.mailbox_id = $1 AND `+folderCondition+`
		   AND e.uid = $3 AND e.expunged_at IS NULL
	`, mailboxID, folderName, int64(uid)).Scan(&messageID, &receivedAt, &folderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}

	var changed int64
	if seen {
		tag, err := tx.Exec(ctx, `
			INSERT INTO message_flags (message_id, received_at, flag)
			VALUES ($1, $2, '\Seen') ON CONFLICT DO NOTHING
		`, messageID, receivedAt)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected()
	} else {
		tag, err := tx.Exec(ctx, `
			DELETE FROM message_flags WHERE message_id = $1 AND received_at = $2 AND flag = '\Seen'
		`, messageID, receivedAt)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected()
	}

	// Only a real transition moves the counter, so opening an already-read
	// message repeatedly cannot drive unread_count negative.
	if changed > 0 {
		delta := 1
		if seen {
			delta = -1
		}
		if _, err := tx.Exec(ctx, `
			UPDATE folders SET unread_count = GREATEST(0, unread_count + $2), highest_modseq = highest_modseq + 1
			 WHERE id = $1
		`, folderID, delta); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
