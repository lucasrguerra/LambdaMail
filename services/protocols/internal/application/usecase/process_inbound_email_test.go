package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/application/port"
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

func TestUseCase_ResolveRecipient_ReturnsRecordForActiveMailbox(t *testing.T) {
	mailboxID := uuid.New()
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		"user@example.test": {ID: mailboxID, MaxMessageBytes: 1000},
	}}
	uc := NewProcessInboundEmailUseCase(mailboxes, nil, nil)

	rec, err := uc.ResolveRecipient(context.Background(), "user@example.test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.ID != mailboxID {
		t.Errorf("ID = %v, want %v", rec.ID, mailboxID)
	}
}

func TestUseCase_ResolveRecipient_ReturnsErrRecipientNotFoundForUnknownAddress(t *testing.T) {
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{}}
	uc := NewProcessInboundEmailUseCase(mailboxes, nil, nil)

	_, err := uc.ResolveRecipient(context.Background(), "nobody@example.test")
	if !errors.Is(err, ErrRecipientNotFound) {
		t.Fatalf("error = %v, want ErrRecipientNotFound", err)
	}
}

type stubBlobStorage struct {
	ref port.BlobRef
	err error
}

func (s *stubBlobStorage) Store(_ context.Context, r io.Reader) (port.BlobRef, error) {
	_, _ = io.Copy(io.Discard, r)
	return s.ref, s.err
}

type recordingMessageRepository struct {
	persisted []port.PersistInboundMessageInput
	nextUID   int64
}

func (r *recordingMessageRepository) Persist(_ context.Context, input port.PersistInboundMessageInput) (int64, error) {
	r.persisted = append(r.persisted, input)
	r.nextUID++
	return r.nextUID, nil
}

func TestUseCase_Handle_StoresBlobOnceAndPersistsOncePerRecipient(t *testing.T) {
	blobRef := port.BlobRef{ID: uuid.New(), SHA256: "abc123", SizeBytes: 42}
	blobs := &stubBlobStorage{ref: blobRef}
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, blobs, messages)

	recipients := []port.MailboxRecord{
		{ID: uuid.New(), MaxMessageBytes: 1000},
		{ID: uuid.New(), MaxMessageBytes: 1000},
	}
	addresses := []string{"a@example.test", "b@example.test"}

	err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "sender@example.test",
		Recipients:         recipients,
		RecipientAddresses: addresses,
		Body:               bytes.NewBufferString("Subject: hi\r\n\r\nbody"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(messages.persisted) != 2 {
		t.Fatalf("persisted %d messages, want 2 (one per recipient, blob stored once)", len(messages.persisted))
	}
	for i, p := range messages.persisted {
		if p.Blob.ID != blobRef.ID {
			t.Errorf("persisted[%d].Blob.ID = %v, want %v (same blob for every recipient)", i, p.Blob.ID, blobRef.ID)
		}
		if p.MailboxID != recipients[i].ID {
			t.Errorf("persisted[%d].MailboxID = %v, want %v", i, p.MailboxID, recipients[i].ID)
		}
		if p.RecipientAddress != addresses[i] {
			t.Errorf("persisted[%d].RecipientAddress = %q, want %q", i, p.RecipientAddress, addresses[i])
		}
		if p.SPFResult != "none" || p.DKIMResult != "none" || p.DMARCResult != "none" {
			t.Errorf("persisted[%d] auth results = %q/%q/%q, want \"none\"/\"none\"/\"none\" (verification lands in a later sub-project)", i, p.SPFResult, p.DKIMResult, p.DMARCResult)
		}
	}
}

func TestUseCase_Handle_PropagatesBlobStorageError(t *testing.T) {
	blobs := &stubBlobStorage{err: errors.New("disk full")}
	uc := NewProcessInboundEmailUseCase(nil, blobs, &recordingMessageRepository{})

	err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "sender@example.test",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"a@example.test"},
		Body:               bytes.NewBufferString("x"),
	})
	if err == nil {
		t.Fatal("expected error when blob storage fails, got nil")
	}
}
