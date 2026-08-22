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
	// SystemGenerated marks a message this server produced itself - a delivery
	// status notification, for instance - rather than one accepted from a peer.
	//
	// Such a message must not be spam-scanned. It is injected without a client
	// IP, so SPF can only come back "none", and its bounce-shaped content then
	// scores highly: the effect was that a "your mail to X was delayed" warning
	// landed in the sender's own Junk folder, which is precisely where the one
	// notification they need to read must never go. It also has no envelope
	// sender to greylist and nothing to quarantine.
	SystemGenerated bool
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
	reportIngestor *IngestDeliveredReportsUseCase
	rules          *ApplyRulesUseCase
	submission     *ProcessOutboundEmailUseCase
	auth           port.AuthRepository
	replyHost      string
	suppression    *VacationSuppression
}

func NewProcessInboundEmailUseCase(mailboxes port.MailboxRepository, blobs port.BlobStorage, messages port.InboundMessageRepository) *ProcessInboundEmailUseCase {
	return &ProcessInboundEmailUseCase{mailboxes: mailboxes, blobs: blobs, messages: messages}
}

// SetRules enables the mailbox's own filing rules. Optional: without it a
// message is delivered exactly as it was before rules existed.
func (uc *ProcessInboundEmailUseCase) SetRules(rules *ApplyRulesUseCase) {
	uc.rules = rules
}

// SetReportIngestor enables reading DMARC and TLS-RPT reports out of the mail
// that carries them. Optional: with no ingestor a report is delivered like any
// other message, which is what happened before this existed.
func (uc *ProcessInboundEmailUseCase) SetReportIngestor(ingestor *IngestDeliveredReportsUseCase) {
	uc.reportIngestor = ingestor
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
			return rejectDmarc()
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

	if uc.scanner != nil && !input.SystemGenerated {
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
				return rejectVirus(scanRes.VirusName)
			case valueobject.ScanVerdictSpamReject:
				return rejectSpam()
			case valueobject.ScanVerdictGreylist:
				return deferGreylisted()
			case valueobject.ScanVerdictSpamJunk:
				targetFolder = "Junk"
			}
			payload = prependHeaders(payload, scanRes.HeadersToAdd)
		}
	}

	// Reports are read here, after the scan has had its say and before the
	// message is filed, because this is where the recipient list and the
	// finished payload are both in hand.
	//
	// A report that fails to parse is still routed to Reports: the address it
	// was sent to is what makes it one, and leaving a broken report in the
	// inbox instead would put back exactly the noise this removes. The bytes
	// are kept either way, so it can be re-ingested once the cause is fixed.
	var vacationReplies []pendingVacation

	alreadySeen := false
	if uc.reportIngestor != nil {
		outcome, err := uc.reportIngestor.Ingest(ctx, input.RecipientAddresses, payload)
		if err != nil {
			return err
		}
		if outcome.Kind != ReportKindNone {
			targetFolder = ReportsFolderName
			alreadySeen = true
		}
	}

	blob, err := uc.blobs.Store(ctx, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	// Every recipient is handed to the repository at once so the whole
	// delivery commits or none of it does. Persisting them one call at a time
	// would mean a failure halfway through leaves the earlier recipients with
	// a copy while the sender is told to retry - and the retry delivers to
	// them a second time.
	// Parsed once for the whole delivery: every recipient's row carries the
	// same headers, and the list view reads them instead of the blob.
	headers := ExtractMessageHeaders(payload)

	persistInputs := make([]port.PersistInboundMessageInput, 0, len(input.Recipients))
	for i, recipient := range input.Recipients {
		// Each recipient's own rules decide their own copy: two mailboxes on
		// this server receiving the same message may file it differently.
		folder := targetFolder
		var decision RuleDecision
		if uc.rules != nil {
			decision = uc.rules.For(ctx, recipient.ID, input.RecipientAddresses[i], input.Sender, payload)
			if decision.Discard {
				// Accepted and then dropped. The sender is told the message was
				// delivered, because a filing rule is not a rejection - saying
				// otherwise would have their server retry it forever.
				log.Printf("rules: %s discarded a message from %s",
					input.RecipientAddresses[i], input.Sender)
				continue
			}
			if decision.Folder != "" {
				folder = decision.Folder
			}
			// Someone who has already been told recently is not told again:
			// otherwise a conversation of five messages produces five copies
			// of the same notice, and a person replying to the automatic
			// message is answered once more.
			if decision.Vacation != nil && uc.suppression.ShouldReply(ctx, recipient.ID, input.Sender) {
				// The message being answered travels with the reply, so it
				// arrives inside that conversation instead of as a loose note
				// the sender has to match up themselves.
				original := headersOf(payload)
				vacationReplies = append(vacationReplies, pendingVacation{
					To:                 input.Sender,
					From:               input.RecipientAddresses[i],
					Subject:            decision.Vacation.Subject,
					Body:               decision.Vacation.Body,
					OriginalMessageID:  firstHeader(original, "message-id"),
					OriginalReferences: firstHeader(original, "references"),
					OriginalSubject:    firstHeader(original, "subject"),
					MailboxID:          recipient.ID,
				})
			}
		}

		persistInputs = append(persistInputs, port.PersistInboundMessageInput{
			MailboxID:        recipient.ID,
			Blob:             blob,
			SenderAddress:    input.Sender,
			RecipientAddress: input.RecipientAddresses[i],
			TargetFolderName: folder,
			Subject:          headers.Subject,
			Snippet:          headers.Snippet,
			FromDisplayName:  headers.FromDisplayName,
			MessageIDHeader:  headers.MessageID,
			HasAttachments:   headers.HasAttachments,
			AlreadySeen:      alreadySeen,
			SPFResult:        authResult.SPF,
			DKIMResult:       authResult.DKIM,
			DMARCResult:      authResult.DMARC,
		})
	}

	// Every recipient's rules discarded it: nothing to file, and the sender is
	// still told the message was accepted.
	if len(persistInputs) == 0 {
		uc.sendVacationReplies(ctx, vacationReplies)
		return nil
	}

	if _, err := uc.messages.PersistAll(ctx, persistInputs); err != nil {
		return err
	}

	// Only after the message is safely stored: a reply sent for a delivery
	// that then rolled back would answer mail the mailbox never received.
	uc.sendVacationReplies(ctx, vacationReplies)

	// Notifications happen only after the commit: an IDLE client told about a
	// message that then rolled back would show one that does not exist.
	for _, recipient := range input.Recipients {
		if uc.trackerManager != nil && uc.folders != nil {
			folderRec, err := uc.folders.FindByName(ctx, recipient.ID.String(), targetFolder)
			if err == nil && folderRec != nil {
				uc.trackerManager.NotifyNumMessages(folderRec.ID, folderRec.NumMessages)
			}
		}
	}
	return nil
}
