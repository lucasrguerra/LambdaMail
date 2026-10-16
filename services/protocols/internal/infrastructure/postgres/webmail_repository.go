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
	pool  *pgxpool.Pool
	store *MailStore
}

func NewWebmailRepository(pool *pgxpool.Pool) *WebmailRepository {
	return &WebmailRepository{pool: pool, store: NewMailStore(pool)}
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

// The methods below change state. They do not do it themselves: the webmail
// names a message by mailbox, folder name and UID, MailStore by folder id and
// UID, and these translate. Every counter, modseq and quota rule lives in
// MailStore, which IMAP uses too, so the two can no longer disagree.

// messageFolder resolves the folder a message is addressed in. A folder that is
// not there and a message that is not there look the same to the caller, so a
// stranger cannot probe which folders exist.
func (r *WebmailRepository) messageFolder(ctx context.Context, mailboxID, folderName string) (string, error) {
	id, err := r.store.ResolveFolder(ctx, mailboxID, folderName)
	if errors.Is(err, port.ErrFolderMissing) {
		return "", ErrMessageNotFound
	}
	return id, err
}

// MarkSeen sets or clears \Seen. The unread counter follows inside MailStore.
func (r *WebmailRepository) MarkSeen(ctx context.Context, mailboxID, folderName string, uid uint32, seen bool) error {
	return r.setFlag(ctx, mailboxID, folderName, uid, `\Seen`, seen)
}

// MarkFlagged sets or clears \Flagged.
func (r *WebmailRepository) MarkFlagged(ctx context.Context, mailboxID, folderName string, uid uint32, flagged bool) error {
	return r.setFlag(ctx, mailboxID, folderName, uid, `\Flagged`, flagged)
}

func (r *WebmailRepository) setFlag(ctx context.Context, mailboxID, folderName string, uid uint32, flag string, on bool) error {
	folderID, err := r.messageFolder(ctx, mailboxID, folderName)
	if err != nil {
		return err
	}
	op := port.FlagOpAdd
	if !on {
		op = port.FlagOpDel
	}
	_, err = r.store.SetFlags(ctx, folderID, uid, op, []string{flag}, 0)
	return err
}

// Expunge removes one message for good (soft delete) and gives its bytes back.
func (r *WebmailRepository) Expunge(ctx context.Context, mailboxID, folderName string, uid uint32) error {
	folderID, err := r.messageFolder(ctx, mailboxID, folderName)
	if err != nil {
		return err
	}
	removed, err := r.store.Expunge(ctx, folderID, []uint32{uid})
	if err != nil {
		return err
	}
	if removed == 0 {
		return ErrMessageNotFound
	}
	return nil
}

// trashTarget is what MoveToTrash resolves; kept as a constant so the two
// callers cannot drift.
const trashTarget = "trash"

// MoveToTrash is what "delete" means in a mail client: the message goes to the
// Trash folder and keeps its identity. Deleting from Trash itself is Expunge's
// job, so asking to move out of Trash reports port.ErrAlreadyInTrash.
func (r *WebmailRepository) MoveToTrash(ctx context.Context, mailboxID, folderName string, uid uint32) (uint32, error) {
	return r.moveInto(ctx, mailboxID, folderName, uid, trashTarget)
}

// MoveToFolder files one message into a named folder, matched by role or name.
func (r *WebmailRepository) MoveToFolder(ctx context.Context, mailboxID, folderName string, uid uint32, target string) (uint32, error) {
	return r.moveInto(ctx, mailboxID, folderName, uid, target)
}

func (r *WebmailRepository) moveInto(ctx context.Context, mailboxID, folderName string, uid uint32, target string) (uint32, error) {
	sourceID, err := r.messageFolder(ctx, mailboxID, folderName)
	if err != nil {
		return 0, err
	}
	destID, err := r.store.ResolveFolder(ctx, mailboxID, target)
	if errors.Is(err, port.ErrFolderMissing) {
		if target == trashTarget {
			return 0, port.ErrNoTrashFolder
		}
		return 0, port.ErrFolderMissing
	}
	if err != nil {
		return 0, err
	}
	if destID == sourceID {
		return 0, port.ErrAlreadyInTrash
	}
	return r.store.Move(ctx, sourceID, uid, destID)
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

// DeleteFolder removes a folder the owner made, and the mail filed in it. The
// messages are expunged (recoverable) and their bytes go back to the quota,
// both inside MailStore.
func (r *WebmailRepository) DeleteFolder(ctx context.Context, mailboxID, name string) error {
	var folderID string
	err := r.pool.QueryRow(ctx, `
		SELECT id::text FROM folders
		 WHERE mailbox_id = $1 AND LOWER(name) = LOWER($2) AND special_use IS NULL
	`, mailboxID, name).Scan(&folderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.ErrFolderMissing
	}
	if err != nil {
		return err
	}
	return r.store.DeleteFolder(ctx, folderID)
}
