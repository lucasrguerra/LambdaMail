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

func (f *fakeMailboxRepository) ResolveDeliveryTargets(_ context.Context, address string) ([]port.MailboxRecord, error) {
	rec, ok := f.byAddress[address]
	if !ok {
		return []port.MailboxRecord{}, nil
	}
	return []port.MailboxRecord{rec}, nil
}

func TestUseCase_ResolveRecipient_ReturnsRecordForActiveMailbox(t *testing.T) {
	mailboxID := uuid.New()
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		"user@example.test": {ID: mailboxID, MaxMessageBytes: 1000, QuotaBytes: 1000},
	}}
	uc := NewProcessInboundEmailUseCase(mailboxes, nil, nil)

	targets, err := uc.ResolveRecipient(context.Background(), "user@example.test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != mailboxID {
		t.Errorf("targets = %+v, want single record with ID %v", targets, mailboxID)
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

func TestUseCase_ResolveRecipient_ReturnsErrMailboxQuotaExceededWhenFull(t *testing.T) {
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		"full@example.test": {ID: uuid.New(), QuotaBytes: 1000, UsedBytes: 1000},
	}}
	uc := NewProcessInboundEmailUseCase(mailboxes, nil, nil)

	_, err := uc.ResolveRecipient(context.Background(), "full@example.test")
	if !errors.Is(err, ErrMailboxQuotaExceeded) {
		t.Fatalf("error = %v, want ErrMailboxQuotaExceeded", err)
	}
}

func TestUseCase_ResolveRecipient_ReturnsErrMailboxQuotaExceededWhenAnyFanOutTargetIsFull(t *testing.T) {
	mailboxes := &fakeAliasFanOutRepository{targets: []port.MailboxRecord{
		{ID: uuid.New(), QuotaBytes: 1000, UsedBytes: 0},
		{ID: uuid.New(), QuotaBytes: 1000, UsedBytes: 1000},
	}}
	uc := NewProcessInboundEmailUseCase(mailboxes, nil, nil)

	_, err := uc.ResolveRecipient(context.Background(), "alias@example.test")
	if !errors.Is(err, ErrMailboxQuotaExceeded) {
		t.Fatalf("error = %v, want ErrMailboxQuotaExceeded when any fan-out target is full", err)
	}
}

// fakeAliasFanOutRepository simulates an alias resolving to multiple mailboxes.
type fakeAliasFanOutRepository struct {
	targets []port.MailboxRecord
}

func (f *fakeAliasFanOutRepository) FindActiveByAddress(_ context.Context, _ string) (*port.MailboxRecord, error) {
	return nil, nil
}

func (f *fakeAliasFanOutRepository) ResolveDeliveryTargets(_ context.Context, _ string) ([]port.MailboxRecord, error) {
	return f.targets, nil
}

type stubBlobStorage struct {
	ref port.BlobRef
	err error
}

func (s *stubBlobStorage) Store(_ context.Context, r io.Reader) (port.BlobRef, error) {
	_, _ = io.Copy(io.Discard, r)
	return s.ref, s.err
}

// errRepositoryFailure stands in for any persistence error.
var errRepositoryFailure = errors.New("repository is unavailable")

type recordingMessageRepository struct {
	persisted []port.PersistInboundMessageInput
	nextUID   int64
	// failAfter makes PersistAll reject a batch larger than this many
	// recipients, standing in for a partial write in the real repository.
	failAfter int
}

func (r *recordingMessageRepository) PersistAll(_ context.Context, inputs []port.PersistInboundMessageInput) ([]int64, error) {
	// The real repository commits once for the whole batch, so a failure
	// records nothing. Mirroring that here keeps the mock honest.
	if r.failAfter > 0 && len(inputs) > r.failAfter {
		return nil, errRepositoryFailure
	}

	uids := make([]int64, 0, len(inputs))
	for _, input := range inputs {
		r.persisted = append(r.persisted, input)
		r.nextUID++
		uids = append(uids, r.nextUID)
	}
	return uids, nil
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

// One SMTP transaction gets one reply, so a fan-out delivery has to be
// all-or-nothing: if the sender is told to retry, no recipient may already
// hold a copy, or the retry duplicates it for them.
func TestUseCase_Handle_DoesNotPartiallyDeliverOnFanOut(t *testing.T) {
	blobs := &stubBlobStorage{ref: port.BlobRef{ID: uuid.New(), SHA256: "abc123", SizeBytes: 42}}
	messages := &recordingMessageRepository{failAfter: 1}
	uc := NewProcessInboundEmailUseCase(nil, blobs, messages)

	recipients := []port.MailboxRecord{
		{ID: uuid.New()},
		{ID: uuid.New()},
	}

	err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "sender@remote.test",
		Recipients:         recipients,
		RecipientAddresses: []string{"alias@example.test", "alias@example.test"},
		Body:               bytes.NewBufferString("Subject: hi\r\n\r\nbody\r\n"),
	})

	if err == nil {
		t.Fatal("Handle reported success even though persistence failed")
	}
	if len(messages.persisted) != 0 {
		t.Errorf("%d recipients were left with a copy after a failed delivery; the sender's retry would duplicate it", len(messages.persisted))
	}
}
