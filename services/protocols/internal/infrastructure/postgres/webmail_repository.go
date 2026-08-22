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
	// The counts are derived from the messages themselves rather than read out
	// of folders.unread_count/total_count.
	//
	// Those columns are a denormalised cache maintained by every write path,
	// and once any one of them was wrong nothing brought it back: the number
	// the user saw stayed wrong through a reload, a logout and a fresh login,
	// because the wrongness was stored. Deriving here is authoritative and
	// self-healing - a folder can only ever report what is actually in it.
	//
	// The columns stay maintained for IMAP's sake (STATUS answers from them),
	// so this is a correction at the read side, not their removal.
	rows, err := r.pool.Query(ctx, `
		SELECT f.id::text, f.name, COALESCE(f.special_use, ''),
		       COUNT(e.id) FILTER (
		         WHERE NOT EXISTS (SELECT 1 FROM message_flags mf
		                            WHERE mf.message_id = e.id
		                              AND mf.received_at = e.received_at
		                              AND mf.flag = '\Seen')
		       )::int AS unread_count,
		       COUNT(e.id)::int AS total_count
		  FROM folders f
		  LEFT JOIN email_messages e
		         ON e.folder_id = f.id AND e.expunged_at IS NULL
		 WHERE f.mailbox_id = $1
		 GROUP BY f.id, f.name, f.special_use
		 ORDER BY CASE f.special_use
		            WHEN 'inbox' THEN 0 WHEN 'drafts' THEN 1 WHEN 'sent' THEN 2
		            WHEN 'archive' THEN 3 WHEN 'junk' THEN 4 WHEN 'trash' THEN 5
		            ELSE 6 END, f.name
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

// Expunge marks one message deleted and keeps the folder counters in step.
//
// Soft delete, matching the expunged_at column the rest of the schema uses: the
// blob stays referenced until the retention sweep, so a mistaken delete is
// recoverable and IMAP clients holding the UID still resolve it.
//
// The message is located by mailbox as well as UID, so a caller cannot expunge
// somebody else's mail by guessing a number.
func (r *WebmailRepository) Expunge(
	ctx context.Context, mailboxID, folderName string, uid uint32,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var messageID uuid.UUID
	var folderID string
	var wasUnread bool
	err = tx.QueryRow(ctx, `
		SELECT e.id, f.id::text,
		       NOT EXISTS (SELECT 1 FROM message_flags mf
		                    WHERE mf.message_id = e.id
		                      AND mf.received_at = e.received_at
		                      AND mf.flag = '\Seen')
		  FROM email_messages e
		  JOIN folders f ON f.id = e.folder_id
		 WHERE e.mailbox_id = $1 AND `+folderCondition+`
		   AND e.uid = $3 AND e.expunged_at IS NULL
	`, mailboxID, folderName, int64(uid)).Scan(&messageID, &folderID, &wasUnread)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMessageNotFound
	}
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE email_messages SET expunged_at = NOW() WHERE id = $1`, messageID); err != nil {
		return err
	}

	// GREATEST guards the counters against ever going negative, which a
	// double expunge of the same row would otherwise cause.
	unreadDelta := 0
	if wasUnread {
		unreadDelta = -1
	}
	if _, err := tx.Exec(ctx, `
		UPDATE folders
		   SET total_count = GREATEST(0, total_count - 1),
		       unread_count = GREATEST(0, unread_count + $2),
		       highest_modseq = highest_modseq + 1
		 WHERE id = $1
	`, folderID, unreadDelta); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// MoveToTrash relocates one message into the mailbox's Trash folder and
// returns the UID it was given there.
//
// This is what "delete" means in a mail client, and the webmail had no way to
// do it at all: the only removal in the codebase was Expunge, reachable solely
// when an autosave superseded a draft. A message the user wanted rid of - the
// empty draft left behind by a sent message, most visibly - could not be
// removed from any screen.
//
// The message keeps its identity and its blob; only the folder and the UID
// change, because a UID is meaningful only within one folder. Deleting from
// Trash itself is Expunge's job, not this one's.
func (r *WebmailRepository) MoveToTrash(
	ctx context.Context, mailboxID, folderName string, uid uint32,
) (uint32, error) {
	return r.moveInto(ctx, mailboxID, folderName, uid, trashTarget)
}

// MoveToFolder files one message into a named folder.
//
// The target is matched on its special-use role or its name, the same way the
// rest of this repository addresses folders, so "archive" and "Archive" both
// reach the same place and a folder the user created themselves - which has no
// role - is reachable by name.
func (r *WebmailRepository) MoveToFolder(
	ctx context.Context, mailboxID, folderName string, uid uint32, target string,
) (uint32, error) {
	return r.moveInto(ctx, mailboxID, folderName, uid, target)
}

// trashTarget is what MoveToTrash resolves; kept as a constant so the two
// callers cannot drift.
const trashTarget = "trash"

func (r *WebmailRepository) moveInto(
	ctx context.Context, mailboxID, folderName string, uid uint32, target string,
) (uint32, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var messageID uuid.UUID
	var sourceFolderID string
	var wasUnread bool
	err = tx.QueryRow(ctx, `
		SELECT e.id, f.id::text,
		       NOT EXISTS (SELECT 1 FROM message_flags mf
		                    WHERE mf.message_id = e.id
		                      AND mf.received_at = e.received_at
		                      AND mf.flag = '\Seen')
		  FROM email_messages e
		  JOIN folders f ON f.id = e.folder_id
		 WHERE e.mailbox_id = $1 AND `+folderCondition+`
		   AND e.uid = $3 AND e.expunged_at IS NULL
	`, mailboxID, folderName, int64(uid)).Scan(&messageID, &sourceFolderID, &wasUnread)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrMessageNotFound
	}
	if err != nil {
		return 0, err
	}

	// Deleting something already in Trash has no further destination, so the
	// caller is told to expunge instead of the message being moved onto itself.
	var trashFolderID string
	var trashUID int64
	err = tx.QueryRow(ctx, `
		SELECT id::text, uid_next FROM folders
		 WHERE mailbox_id = $1 AND (special_use = LOWER($2) OR LOWER(name) = LOWER($2))
		 ORDER BY special_use IS NOT NULL DESC
		 LIMIT 1
		 FOR UPDATE
	`, mailboxID, target).Scan(&trashFolderID, &trashUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, port.ErrNoTrashFolder
	}
	if err != nil {
		return 0, err
	}
	if trashFolderID == sourceFolderID {
		return 0, port.ErrAlreadyInTrash
	}

	var modseq int64
	if err := tx.QueryRow(ctx, `
		UPDATE folders SET uid_next = uid_next + 1, highest_modseq = highest_modseq + 1,
		       total_count = total_count + 1, unread_count = unread_count + $2
		 WHERE id = $1
		 RETURNING highest_modseq
	`, trashFolderID, boolToInt(wasUnread)).Scan(&modseq); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE email_messages SET folder_id = $2, uid = $3, modseq = $4
		 WHERE id = $1
	`, messageID, trashFolderID, trashUID, modseq); err != nil {
		return 0, err
	}

	unreadDelta := 0
	if wasUnread {
		unreadDelta = -1
	}
	if _, err := tx.Exec(ctx, `
		UPDATE folders
		   SET total_count = GREATEST(0, total_count - 1),
		       unread_count = GREATEST(0, unread_count + $2),
		       highest_modseq = highest_modseq + 1
		 WHERE id = $1
	`, sourceFolderID, unreadDelta); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return uint32(trashUID), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// --- folders the mailbox owner creates for themselves --------------------

// CreateFolder adds a folder with no special-use role.
//
// No role, because IMAP defines roles only for the standard set: a folder a
// user invents is addressed by name, which is why the move and the rules match
// on names as well as roles.
func (r *WebmailRepository) CreateFolder(ctx context.Context, mailboxID, name string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1, $2, NULL)
	`, mailboxID, name)
	if err != nil {
		return fmt.Errorf("create folder %q: %w", name, err)
	}
	return nil
}

// RenameFolder changes a folder's name, keeping its messages and its UIDs.
//
// The messages are not touched: a UID belongs to a folder, and the folder is
// still the same one, so renaming must not renumber anything or an IMAP client
// holding those UIDs would lose track of every message in it.
func (r *WebmailRepository) RenameFolder(ctx context.Context, mailboxID, from, to string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE folders SET name = $3
		 WHERE mailbox_id = $1 AND LOWER(name) = LOWER($2) AND special_use IS NULL
	`, mailboxID, from, to)
	if err != nil {
		return fmt.Errorf("rename folder %q: %w", from, err)
	}
	if tag.RowsAffected() == 0 {
		return port.ErrFolderMissing
	}
	return nil
}

// DeleteFolder removes a folder and the mail filed in it.
//
// The messages are expunged rather than deleted outright, matching what the
// rest of the schema does: the blobs stay referenced until the retention sweep,
// so a folder deleted by mistake is still recoverable from the database.
func (r *WebmailRepository) DeleteFolder(ctx context.Context, mailboxID, name string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var folderID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM folders
		 WHERE mailbox_id = $1 AND LOWER(name) = LOWER($2) AND special_use IS NULL
	`, mailboxID, name).Scan(&folderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.ErrFolderMissing
	}
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE email_messages SET expunged_at = NOW()
		 WHERE folder_id = $1 AND expunged_at IS NULL
	`, folderID); err != nil {
		return fmt.Errorf("expunge the folder's messages: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM folders WHERE id = $1`, folderID); err != nil {
		return fmt.Errorf("delete folder %q: %w", name, err)
	}
	return tx.Commit(ctx)
}
