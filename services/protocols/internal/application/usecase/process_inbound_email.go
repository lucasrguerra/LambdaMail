// Package usecase contains the application's use cases (Clean Architecture layer 2).
// Depends only on internal/domain and internal/application/port - no infrastructure.
package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/valueobject"
)

// ErrRecipientNotFound is returned by ResolveRecipient when no active
// mailbox matches - the presentation layer maps this to SMTP 550 5.1.1.
var ErrRecipientNotFound = errors.New("process inbound email: recipient not found or inactive")

// ErrMailboxQuotaExceeded is returned by ResolveRecipient when a resolved
// target mailbox has already reached its storage quota - the presentation
// layer maps this to SMTP 452 4.2.2. If a single RCPT resolves to multiple
// mailboxes (alias fan-out) and any one of them is full, the whole RCPT is
// rejected rather than partially delivering.
var ErrMailboxQuotaExceeded = errors.New("process inbound email: mailbox has reached its storage quota")

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
	scanner        port.ContentScanner
	mailboxes      port.MailboxRepository
	blobs          port.BlobStorage
	messages       port.InboundMessageRepository
	folders        port.ImapFolderRepository
	trackerManager *MailboxTrackerManager
}

func NewProcessInboundEmailUseCase(mailboxes port.MailboxRepository, blobs port.BlobStorage, messages port.InboundMessageRepository) *ProcessInboundEmailUseCase {
	return &ProcessInboundEmailUseCase{mailboxes: mailboxes, blobs: blobs, messages: messages}
}

func (uc *ProcessInboundEmailUseCase) SetScanner(scanner port.ContentScanner) {
	uc.scanner = scanner
}

func (uc *ProcessInboundEmailUseCase) SetTrackerManager(tm *MailboxTrackerManager, folders port.ImapFolderRepository) {
	uc.trackerManager = tm
	uc.folders = folders
}

// ResolveRecipient is called from the SMTP session's RCPT TO handler. It
// returns every mailbox address should be delivered to (more than one for
// an alias that fans out), or ErrRecipientNotFound / ErrMailboxQuotaExceeded.
func (uc *ProcessInboundEmailUseCase) ResolveRecipient(ctx context.Context, address string) ([]port.MailboxRecord, error) {
	targets, err := uc.mailboxes.ResolveDeliveryTargets(ctx, address)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, ErrRecipientNotFound
	}
	for _, t := range targets {
		if t.UsedBytes >= t.QuotaBytes {
			return nil, ErrMailboxQuotaExceeded
		}
	}
	return targets, nil
}

// Handle is called once from the SMTP session's DATA handler, after one or
// more successful RCPT TO calls. It stores the message body exactly once
// and persists exactly one email_messages row per recipient, referencing
// the same blob (PLAN.md section 9's dedup design: 1 email to 50
// recipients = 1 file).
func (uc *ProcessInboundEmailUseCase) Handle(ctx context.Context, input ProcessInboundEmailInput) error {
	payload, err := io.ReadAll(input.Body)
	if err != nil {
		return err
	}

	targetFolder := "INBOX"

	if uc.scanner != nil {
		recipientAddr := ""
		if len(input.RecipientAddresses) > 0 {
			recipientAddr = input.RecipientAddresses[0]
		}
		scanRes, err := uc.scanner.Scan(ctx, port.ScanInput{
			Sender:    input.Sender,
			Recipient: recipientAddr,
			Payload:   payload,
		})
		if err != nil {
			return err
		}
		if scanRes != nil {
			switch scanRes.Verdict {
			case valueobject.ScanVerdictVirusReject:
				return errors.New("554 5.7.1 Virus detected: " + scanRes.VirusName)
			case valueobject.ScanVerdictSpamReject:
				return errors.New("554 5.7.1 Spam threshold exceeded")
			case valueobject.ScanVerdictGreylist:
				return errors.New("451 4.7.1 Greylisted, please try again later")
			case valueobject.ScanVerdictSpamJunk:
				targetFolder = "Junk"
			}
		}
	}

	blob, err := uc.blobs.Store(ctx, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	for i, recipient := range input.Recipients {
		_, err := uc.messages.Persist(ctx, port.PersistInboundMessageInput{
			MailboxID:        recipient.ID,
			Blob:             blob,
			SenderAddress:    input.Sender,
			RecipientAddress: input.RecipientAddresses[i],
			TargetFolderName: targetFolder,
			SPFResult:        "none",
			DKIMResult:       "none",
			DMARCResult:      "none",
		})
		if err != nil {
			return err
		}

		if uc.trackerManager != nil && uc.folders != nil {
			folderRec, err := uc.folders.FindByName(ctx, recipient.ID.String(), targetFolder)
			if err == nil && folderRec != nil {
				uc.trackerManager.NotifyNumMessages(folderRec.ID, folderRec.NumMessages)
			}
		}
	}
	return nil
}
