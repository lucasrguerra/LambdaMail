// Package usecase contains the application's use cases (Clean Architecture layer 2).
// Depends only on internal/domain and internal/application/port - no infrastructure.
package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"

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
	// ClientIP and HeloDomain identify the peer. SPF is defined over them
	// (RFC 7208 section 2.3), so a delivery without them can only ever
	// produce a "none" verdict.
	ClientIP   net.IP
	HeloDomain string
}

// ProcessInboundEmailUseCase implements the inbound half of PLAN.md section
// 3's ProcessInboundEmailUseCase (this sub-project's scope: recipient
// resolution + durable persistence; SPF/DKIM/DMARC/spam/virus checks land in
// later sub-projects).
type ProcessInboundEmailUseCase struct {
	scanner        port.ContentScanner
	authenticator  port.MailAuthenticator
	arcSealer      port.ArcSealer
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

func (uc *ProcessInboundEmailUseCase) SetAuthenticator(authenticator port.MailAuthenticator) {
	uc.authenticator = authenticator
}

// SetArcSealer enables ARC sealing of accepted messages. It is only worth
// enabling on a host that forwards mail onward (PLAN.md section 5).
func (uc *ProcessInboundEmailUseCase) SetArcSealer(sealer port.ArcSealer) {
	uc.arcSealer = sealer
}

func (uc *ProcessInboundEmailUseCase) SetTrackerManager(tm *MailboxTrackerManager, folders port.ImapFolderRepository) {
	uc.trackerManager = tm
	uc.folders = folders
}

// prependHeaders inserts the scanner's verdict headers at the top of the
// message. A receiving MTA adds its trace headers above the existing ones
// (RFC 5321 section 4.4), which also keeps the original bytes - and therefore
// any DKIM signature over them - untouched.
func prependHeaders(payload []byte, headers map[string]string) []byte {
	if len(headers) == 0 {
		return payload
	}

	// Sorted so the delivered message is byte-for-byte reproducible instead of
	// depending on Go's randomized map iteration order.
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	for _, name := range names {
		buf.WriteString(name)
		buf.WriteString(": ")
		buf.WriteString(headers[name])
		buf.WriteString("\r\n")
	}
	buf.Write(payload)
	return buf.Bytes()
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
	// The two slices are indexed in lockstep below: one envelope address per
	// resolved mailbox, repeated when an alias fans out. Refusing a mismatch
	// turns a caller's bug into an error instead of an out-of-range panic that
	// would take down the calling worker.
	if len(input.Recipients) != len(input.RecipientAddresses) {
		return fmt.Errorf(
			"process inbound email: %d recipients but %d envelope addresses",
			len(input.Recipients), len(input.RecipientAddresses),
		)
	}

	payload, err := io.ReadAll(input.Body)
	if err != nil {
		return err
	}

	targetFolder := "INBOX"
	authResult := port.InboundAuthResult{
		SPF:   port.AuthResultNone,
		DKIM:  port.AuthResultNone,
		DMARC: port.AuthResultNone,
	}

	// Authentication runs before the content scanners so that the spam filter
	// sees our Authentication-Results header and can score on it
	// (PLAN.md section 6.1).
	if uc.authenticator != nil {
		authResult = uc.authenticator.Authenticate(ctx, port.InboundAuthInput{
			ClientIP:     input.ClientIP,
			HeloDomain:   input.HeloDomain,
			EnvelopeFrom: input.Sender,
			Message:      payload,
		})

		// Honouring a published "p=reject" is the whole point of the sender
		// having published it. Quarantine is left to the spam filter, which
		// files rather than refuses.
		if authResult.DMARC == port.AuthResultFail && authResult.DmarcPolicy == "reject" {
			return errors.New("550 5.7.1 DMARC policy rejects this message")
		}

		if authResult.AuthenticationResults != "" {
			payload = prependHeaders(payload, map[string]string{
				"Authentication-Results": authResult.AuthenticationResults,
			})
		}

		// The ARC set has to be built over the message as it arrived, with
		// this hop's verdict already known, so it is sealed here rather than
		// after the content scanners rewrite headers.
		if uc.arcSealer != nil {
			sealed, err := uc.arcSealer.Seal(ctx, payload, authResult)
			if err != nil {
				// A sealing failure must not lose the message: it is delivered
				// without the ARC set, which is exactly the state it would
				// have been in had sealing not been enabled.
				log.Printf("arc: could not seal message from %s: %v", input.Sender, err)
			} else {
				payload = sealed
			}
		}
	}

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
			payload = prependHeaders(payload, scanRes.HeadersToAdd)
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
			SPFResult:        authResult.SPF,
			DKIMResult:       authResult.DKIM,
			DMARCResult:      authResult.DMARC,
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
