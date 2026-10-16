package usecase

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// recordingMessageRepo stands in for the repository that files a message into
// a folder; it records what it was asked to store.
type recordingMessageRepo struct {
	inputs []port.PersistInboundMessageInput
	err    error
}

func (r *recordingMessageRepo) PersistAll(_ context.Context, in []port.PersistInboundMessageInput) ([]int64, error) {
	if r.err != nil {
		return nil, r.err
	}
	r.inputs = append(r.inputs, in...)
	return make([]int64, len(in)), nil
}

func sentCopyInput(mailboxID uuid.UUID, fileCopy bool) ProcessOutboundEmailInput {
	input := submissionInput(mailboxID, "ana@remote.test", "bruno@other.test")
	input.FileSentCopy = fileCopy
	input.Body = bytes.NewBufferString("From: user@example.test\r\nTo: ana@remote.test\r\nSubject: Aviso de teste\r\nMessage-ID: <abc@example.test>\r\n\r\nbody")
	return input
}

// A mail client submitting over SMTP cannot file a copy in Sent itself (IMAP
// APPEND is not implemented), so the server does it once the message is queued.
func TestSubmit_FilesACopyInSentWhenAsked(t *testing.T) {
	mailboxID := uuid.New()
	messages := &recordingMessageRepo{}
	blobs := &capturingBlobStorage{}

	uc := NewProcessOutboundEmailUseCase(nil, &countingOutboundRepo{}, blobs, nil)
	uc.SetSentCopies(messages)

	if err := uc.Submit(context.Background(), sentCopyInput(mailboxID, true)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if len(messages.inputs) != 1 {
		t.Fatalf("filed %d copies, want exactly 1 (not one per recipient)", len(messages.inputs))
	}
	got := messages.inputs[0]
	if got.MailboxID != mailboxID {
		t.Errorf("copy filed for mailbox %v, want the sender %v", got.MailboxID, mailboxID)
	}
	if got.TargetFolderName != "Sent" {
		t.Errorf("folder = %q, want Sent", got.TargetFolderName)
	}
	if !got.AlreadySeen {
		t.Error("the sender's own copy must not count as unread mail")
	}
	if got.Subject != "Aviso de teste" || got.MessageIDHeader != "abc@example.test" {
		t.Errorf("headers not extracted: subject=%q message-id=%q", got.Subject, got.MessageIDHeader)
	}
	if got.RecipientAddress != "user@example.test" {
		t.Errorf("recipient address = %q, want the sender's own address", got.RecipientAddress)
	}
}

func TestSubmit_SentCopySharesTheStoredMessageInsteadOfDuplicatingIt(t *testing.T) {
	repo := &countingOutboundRepo{}
	messages := &recordingMessageRepo{}

	uc := NewProcessOutboundEmailUseCase(nil, repo, &capturingBlobStorage{}, nil)
	uc.SetSentCopies(messages)
	if err := uc.Submit(context.Background(), sentCopyInput(uuid.New(), true)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if messages.inputs[0].Blob.ID != repo.jobs[0].BlobID {
		t.Error("the Sent copy must reference the same blob as the delivery jobs")
	}
}

// The webmail files its own copy (without the Bcc line the envelope carries),
// so asking it to file another one would put every sent message in Sent twice.
func TestSubmit_DoesNotFileACopyUnlessAsked(t *testing.T) {
	messages := &recordingMessageRepo{}

	uc := NewProcessOutboundEmailUseCase(nil, &countingOutboundRepo{}, &capturingBlobStorage{}, nil)
	uc.SetSentCopies(messages)
	if err := uc.Submit(context.Background(), sentCopyInput(uuid.New(), false)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if len(messages.inputs) != 0 {
		t.Fatalf("filed %d copies although none was requested", len(messages.inputs))
	}
}

// The mail is already queued when the copy is filed; failing the submission
// now would make the client send it again.
func TestSubmit_ASentCopyFailureDoesNotFailTheSubmission(t *testing.T) {
	repo := &countingOutboundRepo{}
	messages := &recordingMessageRepo{err: errors.New("quota exceeded")}

	uc := NewProcessOutboundEmailUseCase(nil, repo, &capturingBlobStorage{}, nil)
	uc.SetSentCopies(messages)

	if err := uc.Submit(context.Background(), sentCopyInput(uuid.New(), true)); err != nil {
		t.Fatalf("Submit must succeed even when the Sent copy cannot be filed: %v", err)
	}
	if len(repo.jobs) != 2 {
		t.Fatalf("the message was not queued: %d jobs", len(repo.jobs))
	}
}

func TestSubmit_ASubmissionRefusedByTheSendLimitLeavesNothingInSent(t *testing.T) {
	messages := &recordingMessageRepo{}

	uc := NewProcessOutboundEmailUseCase(nil, &countingOutboundRepo{recentCount: 199}, &capturingBlobStorage{}, nil)
	uc.SetSentCopies(messages)

	if err := uc.Submit(context.Background(), sentCopyInput(uuid.New(), true)); !errors.Is(err, ErrSendLimitExceeded) {
		t.Fatalf("error = %v, want ErrSendLimitExceeded", err)
	}
	if len(messages.inputs) != 0 {
		t.Fatal("a message that was refused must not appear in Sent")
	}
}

func TestSubmit_WorksWithoutASentCopyRepository(t *testing.T) {
	uc := NewProcessOutboundEmailUseCase(nil, &countingOutboundRepo{}, &capturingBlobStorage{}, nil)

	if err := uc.Submit(context.Background(), sentCopyInput(uuid.New(), true)); err != nil {
		t.Fatalf("Submit without a Sent repository: %v", err)
	}
}

// One rule for every path: a message sent from the webmail and the same message
// sent by an SMTP client leave the same thing behind in Sent. They used to differ
// completely - the webmail filed a copy, SMTP filed nothing - because the rule
// lived in the webmail alone.
func TestSentCopyIsTheSameWhicheverPathSent(t *testing.T) {
	mailboxID := uuid.New()
	messages := &recordingMessageRepository{}
	submission := NewProcessOutboundEmailUseCase(nil, &countingOutboundRepo{}, &capturingBlobStorage{}, nil)
	submission.SetSentCopies(messages)

	account := &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test", DomainName: "example.test", MaxRecipientsPerHour: 100}
	web := &WebmailUseCase{
		repo:       &stubWebmailRepo{mailboxID: mailboxID.String()},
		auth:       &stubAuthRepo{account: account},
		submission: submission,
		localHost:  "mail.example.test",
	}

	if err := web.Send(context.Background(), ComposeInput{From: "me@example.test", To: []string{"you@remote.test"}, Subject: "Aviso", Text: "hi"}); err != nil {
		t.Fatalf("webmail send: %v", err)
	}

	smtp := ProcessOutboundEmailInput{
		MailboxID: mailboxID, SenderAddr: "me@example.test", DomainName: "example.test",
		Recipients: []string{"you@remote.test"}, FileSentCopy: true,
		Body: bytes.NewBufferString("From: me@example.test\r\nTo: you@remote.test\r\nSubject: Aviso\r\n\r\nhi"),
	}
	if err := submission.Submit(context.Background(), smtp); err != nil {
		t.Fatalf("smtp submit: %v", err)
	}

	if len(messages.persisted) != 2 {
		t.Fatalf("want one copy per path (2), got %d: the webmail must not file its own copy as well", len(messages.persisted))
	}
	fromWeb, fromSMTP := messages.persisted[0], messages.persisted[1]
	for _, got := range []port.PersistInboundMessageInput{fromWeb, fromSMTP} {
		if got.TargetFolderName != "Sent" || !got.AlreadySeen || got.MailboxID != mailboxID {
			t.Errorf("a Sent copy is not what the rule says: %+v", got)
		}
		if got.SenderAddress != "me@example.test" || got.RecipientAddress != "me@example.test" {
			t.Errorf("a Sent copy belongs to its sender: %+v", got)
		}
		if got.Subject != "Aviso" {
			t.Errorf("subject = %q, want Aviso", got.Subject)
		}
	}
}
