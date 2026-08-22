package port

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// WebmailFolder is a folder as the message list shows it.
type WebmailFolder struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SpecialUse  string `json:"special_use"`
	UnreadCount int    `json:"unread_count"`
	TotalCount  int    `json:"total_count"`
}

// WebmailMessage is one row of the message list.
//
// This is a read model projected straight from SQL rather than a hydrated
// aggregate, which is the CQRS exception PLAN.md section 2 carves out for
// webmail reads.
type WebmailMessage struct {
	UID            uint32    `json:"uid"`
	Subject        string    `json:"subject"`
	SenderAddress  string    `json:"sender_address"`
	FromName       string    `json:"from_display_name"`
	Snippet        string    `json:"snippet"`
	SizeBytes      int64     `json:"size_bytes"`
	ReceivedAt     time.Time `json:"received_at"`
	HasAttachments bool      `json:"has_attachments"`
	Seen           bool      `json:"seen"`
	Flagged        bool      `json:"flagged"`
	Answered       bool      `json:"answered"`
	SpamVerdict    string    `json:"spam_verdict"`
	DmarcResult    string    `json:"dmarc_result"`
}

// WebmailMessageBody carries what is needed to render one message.
type WebmailMessageBody struct {
	UID    uint32    `json:"uid"`
	BlobID uuid.UUID `json:"-"`
	Raw    []byte    `json:"-"`
}

// WebmailRepository serves the webmail's read side.
//
// Every method takes the authenticated mailbox ID and scopes its query to it.
// Taking only a folder or a UID would let one account read another's mail by
// guessing an identifier, so the ownership check lives in the SQL rather than
// in the caller.
type WebmailRepository interface {
	FindMailboxIDByAddress(ctx context.Context, address string) (string, error)
	ListFolders(ctx context.Context, mailboxID string) ([]WebmailFolder, error)
	// ListMessages returns the folder's messages newest first. search, when
	// non-empty, filters on subject, sender and snippet.
	ListMessages(ctx context.Context, mailboxID, folderName, search string, limit, offset int) ([]WebmailMessage, error)
	// GetMessageBlob resolves one message to its stored blob, or returns
	// ErrMessageNotFound when it does not belong to this mailbox.
	GetMessageBlob(ctx context.Context, mailboxID, folderName string, uid uint32) (uuid.UUID, error)
	// MarkSeen records that the user opened the message.
	MarkSeen(ctx context.Context, mailboxID, folderName string, uid uint32, seen bool) error
	// Expunge soft-deletes one message, keeping the folder counters in step.
	Expunge(ctx context.Context, mailboxID, folderName string, uid uint32) error
	// MoveToFolder relocates one message into a named folder and returns its
	// new UID there. A UID is meaningful only inside one folder, so the
	// message keeps its identity and its blob but is renumbered.
	MoveToFolder(ctx context.Context, mailboxID, folderName string, uid uint32, target string) (uint32, error)
	// MoveToTrash relocates one message into Trash and returns its new UID
	// there. This is what deleting a message means everywhere but in Trash
	// itself, where Expunge is the operation instead.
	MoveToTrash(ctx context.Context, mailboxID, folderName string, uid uint32) (uint32, error)
}

// ErrFolderMissing reports a folder that is not there, or that is a standard
// folder and therefore not the user's to change.
var ErrFolderMissing = errors.New("folders: no such folder for this mailbox")

// ErrNoTrashFolder reports a mailbox with nowhere to put deleted mail, rather
// than the message being destroyed instead.
var ErrNoTrashFolder = errors.New("webmail: this mailbox has no Trash folder")

// ErrAlreadyInTrash tells the caller to expunge rather than move: a message
// already in Trash has no further destination.
var ErrAlreadyInTrash = errors.New("webmail: message is already in Trash")

// FolderAdmin manages the folders a mailbox owner creates for themselves.
//
// Separate from WebmailRepository because these are writes to the folder list
// itself rather than reads of the mail inside it, and because the reserved
// folders are protected in the use case above this, not here.
type FolderAdmin interface {
	ListFolders(ctx context.Context, mailboxID string) ([]WebmailFolder, error)
	CreateFolder(ctx context.Context, mailboxID, name string) error
	RenameFolder(ctx context.Context, mailboxID, from, to string) error
	// DeleteFolder removes the folder and the messages filed in it.
	DeleteFolder(ctx context.Context, mailboxID, name string) error
}
