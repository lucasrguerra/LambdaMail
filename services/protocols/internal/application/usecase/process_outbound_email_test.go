package usecase

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

type fakeAuthRepo struct {
	record *port.MailboxAuth
}

func (f *fakeAuthRepo) FindByAddress(_ context.Context, _ string) (*port.MailboxAuth, error) {
	return f.record, nil
}

type countingOutboundRepo struct {
	fakeOutboundRepo
	recentCount int
}

func (c *countingOutboundRepo) CountRecipientsSince(_ context.Context, _ uuid.UUID, _ time.Time) (int, error) {
	return c.recentCount, nil
}

type recordingSigner struct {
	calledForDomain string
}

func (r *recordingSigner) Sign(_ context.Context, domainName string, message []byte) ([]byte, error) {
	r.calledForDomain = domainName
	return append([]byte("DKIM-Signature: v=1; d="+domainName+"\r\n"), message...), nil
}

func submissionInput(mailboxID uuid.UUID, recipients ...string) ProcessOutboundEmailInput {
	return ProcessOutboundEmailInput{
		MailboxID:            mailboxID,
		SenderAddr:           "user@example.test",
		DomainName:           "example.test",
		Recipients:           recipients,
		Body:                 bytes.NewBufferString("Subject: hi\r\n\r\nbody"),
		MaxRecipientsPerHour: 200,
	}
}

// PLAN.md section 3: submission signs with DKIM and enqueues one job per
// recipient, all referencing a single stored copy of the message.
func TestSubmit_SignsAndEnqueuesOneJobPerRecipient(t *testing.T) {
	repo := &countingOutboundRepo{}
	blobs := &capturingBlobStorage{}
	signer := &recordingSigner{}
	mailboxID := uuid.New()

	uc := NewProcessOutboundEmailUseCase(nil, repo, blobs, signer)
	err := uc.Submit(context.Background(), submissionInput(mailboxID, "a@remote.test", "b@other.test"))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if len(repo.jobs) != 2 {
		t.Fatalf("enqueued %d jobs, want 2", len(repo.jobs))
	}
	if signer.calledForDomain != "example.test" {
		t.Errorf("signed for domain %q, want example.test", signer.calledForDomain)
	}
	if !strings.HasPrefix(string(blobs.stored), "DKIM-Signature:") {
		t.Errorf("the stored message is not the signed one:\n%s", blobs.stored)
	}

	first, second := repo.jobs[0], repo.jobs[1]
	if first.BlobID != second.BlobID {
		t.Error("recipients got separate copies of the message instead of sharing one blob")
	}
	if first.DestinationDomain != "remote.test" || second.DestinationDomain != "other.test" {
		t.Errorf("destination domains = %q, %q", first.DestinationDomain, second.DestinationDomain)
	}
	if first.Status != entity.OutboundJobStatusQueued {
		t.Errorf("status = %s, want QUEUED", first.Status)
	}
	if first.ExpiresAt.Sub(first.NextAttemptAt) < 4*24*time.Hour {
		t.Errorf("delivery window = %v, want the 5 day window of PLAN section 6.3", first.ExpiresAt.Sub(first.NextAttemptAt))
	}
	if first.MailboxID == nil || *first.MailboxID != mailboxID {
		t.Error("job is not attributed to the sending mailbox")
	}
}

// A compromised account must hit the wall before the mail is queued, not
// after (PLAN.md section 5.2).
func TestSubmit_RefusesWhenHourlyRecipientLimitWouldBeExceeded(t *testing.T) {
	repo := &countingOutboundRepo{recentCount: 199}

	uc := NewProcessOutboundEmailUseCase(nil, repo, &capturingBlobStorage{}, nil)
	err := uc.Submit(context.Background(), submissionInput(uuid.New(), "a@remote.test", "b@remote.test"))

	if !errors.Is(err, ErrSendLimitExceeded) {
		t.Fatalf("error = %v, want ErrSendLimitExceeded", err)
	}
	if len(repo.jobs) != 0 {
		t.Errorf("enqueued %d jobs despite exceeding the limit", len(repo.jobs))
	}
}

func TestSubmit_AllowsSubmissionExactlyAtTheLimit(t *testing.T) {
	repo := &countingOutboundRepo{recentCount: 198}

	uc := NewProcessOutboundEmailUseCase(nil, repo, &capturingBlobStorage{}, nil)
	if err := uc.Submit(context.Background(), submissionInput(uuid.New(), "a@remote.test", "b@remote.test")); err != nil {
		t.Fatalf("Submit at exactly the limit: %v", err)
	}
	if len(repo.jobs) != 2 {
		t.Errorf("enqueued %d jobs, want 2", len(repo.jobs))
	}
}

func TestAuthenticate_AcceptsCorrectPasswordAndRejectsWrongOne(t *testing.T) {
	hash, err := argon2id.CreateHash("correct horse battery staple", argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("CreateHash: %v", err)
	}
	repo := &fakeAuthRepo{record: &port.MailboxAuth{
		ID: uuid.New(), PasswordHash: hash, EmailAddress: "user@example.test",
	}}
	uc := NewProcessOutboundEmailUseCase(repo, nil, nil, nil)
	ctx := context.Background()

	if _, err := uc.Authenticate(ctx, "user@example.test", "correct horse battery staple"); err != nil {
		t.Fatalf("Authenticate with the right password: %v", err)
	}
	if _, err := uc.Authenticate(ctx, "user@example.test", "wrong"); !errors.Is(err, ErrSubmissionAuthFailed) {
		t.Errorf("error = %v, want ErrSubmissionAuthFailed", err)
	}
}

// An unknown mailbox and a wrong password must be indistinguishable, so the
// endpoint cannot be used to enumerate accounts.
func TestAuthenticate_UnknownMailboxYieldsTheSameError(t *testing.T) {
	uc := NewProcessOutboundEmailUseCase(&fakeAuthRepo{record: nil}, nil, nil, nil)

	if _, err := uc.Authenticate(context.Background(), "nobody@example.test", "x"); !errors.Is(err, ErrSubmissionAuthFailed) {
		t.Fatalf("error = %v, want ErrSubmissionAuthFailed", err)
	}
}

// Without this check any authenticated account could forge mail from any
// other address the server hosts.
func TestAuthorizeSender_RejectsForeignEnvelopeSender(t *testing.T) {
	session := &port.MailboxAuth{EmailAddress: "user@example.test"}

	if err := AuthorizeSender(session, "user@example.test"); err != nil {
		t.Errorf("sending as itself was refused: %v", err)
	}
	if err := AuthorizeSender(session, "USER@EXAMPLE.TEST"); err != nil {
		t.Errorf("address comparison must be case insensitive: %v", err)
	}
	if err := AuthorizeSender(session, "ceo@example.test"); !errors.Is(err, ErrSenderNotOwned) {
		t.Errorf("error = %v, want ErrSenderNotOwned", err)
	}
	if err := AuthorizeSender(nil, "user@example.test"); !errors.Is(err, ErrSubmissionAuthFailed) {
		t.Errorf("error = %v, want ErrSubmissionAuthFailed for an unauthenticated session", err)
	}
}
