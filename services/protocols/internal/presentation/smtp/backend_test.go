package smtppresentation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	gosmtp "github.com/emersion/go-smtp"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

type fakeMailboxRepository struct {
	byAddress map[string]port.MailboxRecord
}

func (f *fakeMailboxRepository) FindActiveByAddress(_ context.Context, address string) (*port.MailboxRecord, error) {
	rec, ok := f.byAddress[address]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

func (f *fakeMailboxRepository) ResolveDeliveryTargets(_ context.Context, address string) ([]port.MailboxRecord, error) {
	rec, ok := f.byAddress[address]
	if !ok {
		return []port.MailboxRecord{}, nil
	}
	return []port.MailboxRecord{rec}, nil
}

type recordingBlobStorage struct {
	stored []byte
	ref    port.BlobRef
}

func (b *recordingBlobStorage) Store(_ context.Context, r io.Reader) (port.BlobRef, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return port.BlobRef{}, err
	}
	b.stored = data
	return b.ref, nil
}

type recordingMessageRepository struct {
	persisted []port.PersistInboundMessageInput
}

func (m *recordingMessageRepository) Persist(_ context.Context, input port.PersistInboundMessageInput) (int64, error) {
	m.persisted = append(m.persisted, input)
	return int64(len(m.persisted)), nil
}

func newTestSession(mailboxes *fakeMailboxRepository, blobs *recordingBlobStorage, messages *recordingMessageRepository) gosmtp.Session {
	uc := usecase.NewProcessInboundEmailUseCase(mailboxes, blobs, messages)
	backend := NewBackend(uc)
	session, err := backend.NewSession(nil)
	if err != nil {
		panic(err)
	}
	return session
}

func TestSession_Rcpt_AcceptsKnownActiveRecipient(t *testing.T) {
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		"user@example.test": {ID: uuid.New(), MaxMessageBytes: 1000, QuotaBytes: 1000},
	}}
	session := newTestSession(mailboxes, &recordingBlobStorage{}, &recordingMessageRepository{})

	if err := session.Mail("sender@example.test", &gosmtp.MailOptions{}); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if err := session.Rcpt("user@example.test", &gosmtp.RcptOptions{}); err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
}

func TestSession_Rcpt_Rejects550ForUnknownRecipient(t *testing.T) {
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{}}
	session := newTestSession(mailboxes, &recordingBlobStorage{}, &recordingMessageRepository{})

	_ = session.Mail("sender@example.test", &gosmtp.MailOptions{})
	err := session.Rcpt("nobody@example.test", &gosmtp.RcptOptions{})
	if err == nil {
		t.Fatal("expected an error for an unknown recipient, got nil")
	}
	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("error is not a *gosmtp.SMTPError: %v", err)
	}
	if smtpErr.Code != 550 {
		t.Errorf("Code = %d, want 550", smtpErr.Code)
	}
}

func TestSession_Data_PersistsOnceForSingleRecipient(t *testing.T) {
	mailboxID := uuid.New()
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		"user@example.test": {ID: mailboxID, MaxMessageBytes: 1000, QuotaBytes: 1000},
	}}
	blobs := &recordingBlobStorage{ref: port.BlobRef{ID: uuid.New(), SHA256: "abc", SizeBytes: 5}}
	messages := &recordingMessageRepository{}
	session := newTestSession(mailboxes, blobs, messages)

	_ = session.Mail("sender@example.test", &gosmtp.MailOptions{})
	_ = session.Rcpt("user@example.test", &gosmtp.RcptOptions{})

	body := "Subject: hi\r\n\r\nhello"
	if err := session.Data(bytes.NewBufferString(body)); err != nil {
		t.Fatalf("Data: %v", err)
	}

	if string(blobs.stored) != body {
		t.Errorf("stored blob = %q, want %q", blobs.stored, body)
	}
	if len(messages.persisted) != 1 {
		t.Fatalf("persisted %d messages, want 1", len(messages.persisted))
	}
	if messages.persisted[0].MailboxID != mailboxID {
		t.Errorf("persisted MailboxID = %v, want %v", messages.persisted[0].MailboxID, mailboxID)
	}
}

func TestSession_Rcpt_Rejects452ForFullMailbox(t *testing.T) {
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		"full@example.test": {ID: uuid.New(), QuotaBytes: 1000, UsedBytes: 1000},
	}}
	session := newTestSession(mailboxes, &recordingBlobStorage{}, &recordingMessageRepository{})

	_ = session.Mail("sender@example.test", &gosmtp.MailOptions{})
	err := session.Rcpt("full@example.test", &gosmtp.RcptOptions{})
	if err == nil {
		t.Fatal("expected an error for a full mailbox, got nil")
	}
	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("error is not a *gosmtp.SMTPError: %v", err)
	}
	if smtpErr.Code != 452 {
		t.Errorf("Code = %d, want 452", smtpErr.Code)
	}
}

func TestSession_Rcpt_DeduplicatesRepeatedAddress(t *testing.T) {
	mailboxID := uuid.New()
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		"user@example.test": {ID: mailboxID, MaxMessageBytes: 1000, QuotaBytes: 1000},
	}}
	blobs := &recordingBlobStorage{ref: port.BlobRef{ID: uuid.New(), SHA256: "abc", SizeBytes: 5}}
	messages := &recordingMessageRepository{}
	session := newTestSession(mailboxes, blobs, messages)

	_ = session.Mail("sender@example.test", &gosmtp.MailOptions{})
	if err := session.Rcpt("user@example.test", &gosmtp.RcptOptions{}); err != nil {
		t.Fatalf("first Rcpt: %v", err)
	}
	if err := session.Rcpt("user@example.test", &gosmtp.RcptOptions{}); err != nil {
		t.Fatalf("duplicate Rcpt: %v", err)
	}

	if err := session.Data(bytes.NewBufferString("Subject: hi\r\n\r\nhello")); err != nil {
		t.Fatalf("Data: %v", err)
	}

	if len(messages.persisted) != 1 {
		t.Fatalf("persisted %d messages, want 1 (duplicate RCPT should not double-persist)", len(messages.persisted))
	}
}

func TestSession_Reset_ClearsAccumulatedRecipients(t *testing.T) {
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		"user@example.test": {ID: uuid.New(), MaxMessageBytes: 1000, QuotaBytes: 1000},
	}}
	messages := &recordingMessageRepository{}
	session := newTestSession(mailboxes, &recordingBlobStorage{}, messages)

	_ = session.Mail("sender@example.test", &gosmtp.MailOptions{})
	_ = session.Rcpt("user@example.test", &gosmtp.RcptOptions{})
	session.Reset()

	// After Reset, Data with no prior Mail/Rcpt in this transaction must not
	// silently persist anything.
	err := session.Data(bytes.NewBufferString("Subject: x\r\n\r\nbody"))
	if err == nil {
		t.Fatal("expected an error calling Data after Reset with no recipients, got nil")
	}
	if len(messages.persisted) != 0 {
		t.Errorf("persisted %d messages after Reset, want 0", len(messages.persisted))
	}
}
