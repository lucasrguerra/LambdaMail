// Package usecase contains the application's use cases (Clean Architecture layer 2).
// Depends only on internal/domain and internal/application/port - no infrastructure.
package usecase

import (
	"context"
	"errors"
	"io"

	"lambdamail/protocols/internal/application/port"
)

// ErrRecipientNotFound is returned by ResolveRecipient when no active
// mailbox matches - the presentation layer maps this to SMTP 550 5.1.1.
var ErrRecipientNotFound = errors.New("process inbound email: recipient not found or inactive")

// ProcessInboundEmailInput carries one SMTP DATA transaction's worth of
// state: the accumulated recipients from one or more successful Rcpt calls,
// and the message body stream.
type ProcessInboundEmailInput struct {
	Sender             string
	Recipients         []port.MailboxRecord
	RecipientAddresses []string
	Body               io.Reader
}

// ProcessInboundEmailUseCase implements the inbound half of PLAN.md section
// 3's ProcessInboundEmailUseCase (this sub-project's scope: recipient
// resolution + durable persistence; SPF/DKIM/DMARC/spam/virus checks land in
// later sub-projects).
type ProcessInboundEmailUseCase struct {
	mailboxes port.MailboxRepository
	blobs     port.BlobStorage
	messages  port.InboundMessageRepository
}

func NewProcessInboundEmailUseCase(mailboxes port.MailboxRepository, blobs port.BlobStorage, messages port.InboundMessageRepository) *ProcessInboundEmailUseCase {
	return &ProcessInboundEmailUseCase{mailboxes: mailboxes, blobs: blobs, messages: messages}
}

// ResolveRecipient is called from the SMTP session's RCPT TO handler.
func (uc *ProcessInboundEmailUseCase) ResolveRecipient(ctx context.Context, address string) (*port.MailboxRecord, error) {
	rec, err := uc.mailboxes.FindActiveByAddress(ctx, address)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, ErrRecipientNotFound
	}
	return rec, nil
}

// Handle is called once from the SMTP session's DATA handler, after one or
// more successful RCPT TO calls. It stores the message body exactly once
// and persists exactly one email_messages row per recipient, referencing
// the same blob (PLAN.md section 9's dedup design: 1 email to 50
// recipients = 1 file).
func (uc *ProcessInboundEmailUseCase) Handle(ctx context.Context, input ProcessInboundEmailInput) error {
	blob, err := uc.blobs.Store(ctx, input.Body)
	if err != nil {
		return err
	}

	for i, recipient := range input.Recipients {
		_, err := uc.messages.Persist(ctx, port.PersistInboundMessageInput{
			MailboxID:        recipient.ID,
			Blob:             blob,
			SenderAddress:    input.Sender,
			RecipientAddress: input.RecipientAddresses[i],
			SPFResult:        "none",
			DKIMResult:       "none",
			DMARCResult:      "none",
		})
		if err != nil {
			return err
		}
	}
	return nil
}
