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
	SPFResult        string
	DKIMResult       string
	DMARCResult      string
}

// InboundMessageRepository durably records an accepted inbound message for
// one recipient: allocates the next folder UID, inserts the email_messages
// row, increments the blob's ref_count, and writes the transactional outbox
// event - all in a single transaction (PLAN.md section 6.1, section 9.4).
type InboundMessageRepository interface {
	// Persist returns the allocated IMAP UID on success. The caller may only
	// report success to the SMTP client after this returns without error.
	Persist(ctx context.Context, input PersistInboundMessageInput) (uid int64, err error)
}
