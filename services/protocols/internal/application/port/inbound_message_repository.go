package port

import (
	"context"

	"github.com/google/uuid"
)

// PersistInboundMessageInput is everything needed to durably record one
// recipient's copy of an accepted inbound message.
type PersistInboundMessageInput struct {
	MailboxID        uuid.UUID
	Blob             BlobRef
	SenderAddress    string
	RecipientAddress string
	TargetFolderName string // "INBOX", "Junk", etc. (defaults to "INBOX" if empty)
	// Header-derived fields. The columns for these have existed since the
	// first migration but nothing filled them, so every message listed in the
	// webmail had a blank subject and sender name.
	Subject         string
	Snippet         string
	FromDisplayName string
	MessageIDHeader string
	HasAttachments  bool
	// AlreadySeen marks a copy the user themselves composed - their own Sent
	// copy, or a draft. It is not unread mail, and counting it as such is why
	// the Sent folder carried an unread badge that nothing could clear: no
	// reader ever opens their own outgoing copy, so the flag was never set.
	AlreadySeen bool
	SPFResult   string
	DKIMResult      string
	DMARCResult     string
}

// InboundMessageRepository durably records an accepted inbound message: for
// each recipient it allocates the next folder UID, inserts the email_messages
// row, increments the blob's ref_count, and writes the transactional outbox
// event (PLAN.md section 6.1, section 9.4).
type InboundMessageRepository interface {
	// PersistAll records every recipient's copy in a single transaction and
	// returns the allocated IMAP UIDs, in the order the inputs were given.
	//
	// One transaction for the whole delivery is a correctness requirement,
	// not an optimisation. An alias fanning out to several mailboxes is one
	// SMTP transaction with one reply: if the second insert failed after the
	// first had committed, the sender would be told to retry and the first
	// recipient would receive the message twice. Either all recipients get
	// it, or the sender is asked to try again.
	//
	// The caller may only report success to the SMTP client after this
	// returns without error.
	PersistAll(ctx context.Context, inputs []PersistInboundMessageInput) (uids []int64, err error)
}
