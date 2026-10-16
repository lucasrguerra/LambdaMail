package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

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

// The methods below only read. They do not query on their own: the webmail asks
// MailStore the same questions IMAP does and shapes the answers for the
// browser, so a folder shows the same numbers in both.

func (r *WebmailRepository) FindMailboxIDByAddress(ctx context.Context, address string) (string, error) {
	return r.store.MailboxIDByAddress(ctx, address)
}

// roleOrder is how the folder list is arranged for people: the folders they use
// every day first, in a fixed order, then their own by name.
var roleOrder = map[string]int{"inbox": 0, "drafts": 1, "sent": 2, "archive": 3, "junk": 4, "trash": 5}

func (r *WebmailRepository) ListFolders(ctx context.Context, mailboxID string) ([]port.WebmailFolder, error) {
	folders, err := r.store.Folders(ctx, mailboxID)
	if err != nil {
		return nil, err
	}

	out := make([]port.WebmailFolder, 0, len(folders))
	for _, f := range folders {
		out = append(out, port.WebmailFolder{ID: f.ID, Name: f.Name, SpecialUse: f.SpecialUse, UnreadCount: f.Unread, TotalCount: f.Total})
	}

	rank := func(f port.WebmailFolder) int {
		if n, ok := roleOrder[f.SpecialUse]; ok {
			return n
		}
		return 6
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (r *WebmailRepository) ListMessages(
	ctx context.Context, mailboxID, folderName, search string, limit, offset int,
) ([]port.WebmailMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	folderID, err := r.store.ResolveFolder(ctx, mailboxID, folderName)
	if errors.Is(err, port.ErrFolderMissing) {
		return []port.WebmailMessage{}, nil
	}
	if err != nil {
		return nil, err
	}

	messages, err := r.store.Messages(ctx, folderID, MessageQuery{NewestFirst: true, Search: search, Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}

	out := make([]port.WebmailMessage, 0, len(messages))
	for _, m := range messages {
		out = append(out, port.WebmailMessage{
			UID: m.UID, Subject: m.Subject, SenderAddress: m.SenderAddress, FromName: m.FromName, Snippet: m.Snippet,
			SizeBytes: m.Size, ReceivedAt: m.ReceivedAt, HasAttachments: m.HasAttachments,
			Seen: m.HasFlag(`\Seen`), Flagged: m.HasFlag(`\Flagged`), Answered: m.HasFlag(`\Answered`),
			SpamVerdict: m.SpamVerdict, DmarcResult: m.DmarcResult,
		})
	}
	return out, nil
}

func (r *WebmailRepository) GetMessageBlob(ctx context.Context, mailboxID, folderName string, uid uint32) (uuid.UUID, error) {
	folderID, err := r.messageFolder(ctx, mailboxID, folderName)
	if err != nil {
		return uuid.Nil, err
	}
	return r.store.MessageBlob(ctx, folderID, uid)
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
