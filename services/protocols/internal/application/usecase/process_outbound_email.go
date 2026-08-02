package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

var (
	// ErrSubmissionAuthFailed is returned for both an unknown mailbox and a
	// wrong password: telling the two apart would turn submission into an
	// account-enumeration oracle.
	ErrSubmissionAuthFailed = errors.New("process outbound email: authentication failed")

	// ErrSendLimitExceeded is returned when a mailbox has queued more
	// recipients in the last hour than its policy allows.
	ErrSendLimitExceeded = errors.New("process outbound email: sender hourly recipient limit exceeded")

	// ErrSenderNotOwned is returned when the authenticated mailbox tries to
	// send as somebody else.
	ErrSenderNotOwned = errors.New("process outbound email: envelope sender does not belong to the authenticated mailbox")
)

// deliveryWindow is how long a job may keep retrying before it bounces
// (PLAN.md section 6.3).
const deliveryWindow = 5 * 24 * time.Hour

// sendLimitWindow is the period the per-mailbox recipient limit applies over.
const sendLimitWindow = time.Hour

// ProcessOutboundEmailInput is one authenticated submission.
type ProcessOutboundEmailInput struct {
	MailboxID  uuid.UUID
	SenderAddr string
	DomainName string
	Recipients []string
	Body       io.Reader
	// MaxRecipientsPerHour is the sender's policy, carried from the
	// authenticated session.
	MaxRecipientsPerHour int
}

// ProcessOutboundEmailUseCase implements the submission half of PLAN.md
// section 3: authenticate, apply send limits, sign with DKIM, enqueue.
type ProcessOutboundEmailUseCase struct {
	auth     port.AuthRepository
	outbound port.OutboundRepository
	blobs    port.BlobStorage
	signer   port.DkimSigner
}

func NewProcessOutboundEmailUseCase(
	auth port.AuthRepository,
	outbound port.OutboundRepository,
	blobs port.BlobStorage,
	signer port.DkimSigner,
) *ProcessOutboundEmailUseCase {
	return &ProcessOutboundEmailUseCase{auth: auth, outbound: outbound, blobs: blobs, signer: signer}
}

// Authenticate verifies a submission credential and returns the mailbox the
// session may then send as.
func (uc *ProcessOutboundEmailUseCase) Authenticate(ctx context.Context, address, password string) (*port.MailboxAuth, error) {
	rec, err := uc.auth.FindByAddress(ctx, address)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		// Still spend the time a real comparison would, so that a missing
		// mailbox is not distinguishable by response time.
		_, _ = argon2id.ComparePasswordAndHash(password, decoyHash)
		return nil, ErrSubmissionAuthFailed
	}

	match, err := argon2id.ComparePasswordAndHash(password, rec.PasswordHash)
	if err != nil || !match {
		return nil, ErrSubmissionAuthFailed
	}
	return rec, nil
}

// decoyHash is a well-formed Argon2id hash of a value nobody knows. It exists
// only to keep the timing of a failed lookup close to that of a real check.
const decoyHash = "$argon2id$v=19$m=65536,t=3,p=2$YWJjZGVmZ2hpamtsbW5vcA$Zm9yY2VkLWRlY295LWNvbXBhcmlzb24taGFzaA"

// Submit signs the message and queues one delivery job per recipient. The
// message is stored once and every job references the same blob, matching the
// deduplication design of PLAN.md section 9.
func (uc *ProcessOutboundEmailUseCase) Submit(ctx context.Context, input ProcessOutboundEmailInput) error {
	if len(input.Recipients) == 0 {
		return fmt.Errorf("process outbound email: no recipients")
	}

	if err := uc.checkSendLimit(ctx, input); err != nil {
		return err
	}

	payload, err := io.ReadAll(input.Body)
	if err != nil {
		return fmt.Errorf("read submitted message: %w", err)
	}

	if uc.signer != nil {
		signed, err := uc.signer.Sign(ctx, input.DomainName, payload)
		if err != nil {
			return fmt.Errorf("sign submitted message: %w", err)
		}
		payload = signed
	}

	blob, err := uc.blobs.Store(ctx, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("store submitted message: %w", err)
	}

	now := time.Now()
	mailboxID := input.MailboxID

	for _, recipient := range input.Recipients {
		job := &entity.OutboundJob{
			MailboxID:         &mailboxID,
			BlobID:            blob.ID,
			EnvelopeFrom:      input.SenderAddr,
			EnvelopeTo:        recipient,
			DestinationDomain: recipientDomain(recipient),
			Status:            entity.OutboundJobStatusQueued,
			Attempt:           0,
			NextAttemptAt:     now,
			ExpiresAt:         now.Add(deliveryWindow),
		}
		if err := uc.outbound.Enqueue(ctx, job); err != nil {
			return fmt.Errorf("enqueue delivery to %s: %w", recipient, err)
		}
	}

	return nil
}

// checkSendLimit refuses the whole submission when accepting it would take the
// mailbox past its hourly recipient budget. Refusing up front is what contains
// a compromised account; accepting and dropping later would not.
func (uc *ProcessOutboundEmailUseCase) checkSendLimit(ctx context.Context, input ProcessOutboundEmailInput) error {
	if input.MaxRecipientsPerHour <= 0 {
		return nil
	}

	alreadySent, err := uc.outbound.CountRecipientsSince(ctx, input.MailboxID, time.Now().Add(-sendLimitWindow))
	if err != nil {
		return fmt.Errorf("check send limit: %w", err)
	}

	if alreadySent+len(input.Recipients) > input.MaxRecipientsPerHour {
		return fmt.Errorf("%w: %d already sent plus %d requested exceeds %d",
			ErrSendLimitExceeded, alreadySent, len(input.Recipients), input.MaxRecipientsPerHour)
	}
	return nil
}

// AuthorizeSender enforces that a session sends only as itself. Without this
// any account could forge any other address on the server.
func AuthorizeSender(session *port.MailboxAuth, envelopeFrom string) error {
	if session == nil {
		return ErrSubmissionAuthFailed
	}
	if !strings.EqualFold(strings.TrimSpace(envelopeFrom), session.EmailAddress) {
		return ErrSenderNotOwned
	}
	return nil
}

func recipientDomain(address string) string {
	if idx := strings.LastIndex(address, "@"); idx >= 0 && idx < len(address)-1 {
		return strings.ToLower(address[idx+1:])
	}
	return ""
}
