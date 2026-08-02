package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

// countingMXResolver fails the test if delivery ever tries to leave the host.
type countingMXResolver struct{ lookups int }

func (c *countingMXResolver) LookupMX(_ context.Context, _ string) ([]string, error) {
	c.lookups++
	// An address that cannot be dialled, so a test that does reach here fails
	// on the assertion rather than on a timeout.
	return []string{"127.0.0.1:9"}, nil
}

func newLocalDeliveryJob(to string) *entity.OutboundJob {
	mbID := uuid.New()
	return &entity.OutboundJob{
		ID:                uuid.New(),
		MailboxID:         &mbID,
		BlobID:            uuid.New(),
		EnvelopeFrom:      "me@example.test",
		EnvelopeTo:        to,
		DestinationDomain: "example.test",
		Status:            entity.OutboundJobStatusQueued,
		NextAttemptAt:     time.Now(),
		ExpiresAt:         time.Now().Add(time.Hour),
	}
}

// Mail between two accounts on this server must not depend on outbound port
// 25. Every message used to be resolved through MX and delivered over SMTP,
// including one addressed to a mailbox on this very host - so on a provider
// that blocks port 25, users could not write to each other on their own
// server.
func TestLocalRecipientIsDeliveredWithoutLeavingTheHost(t *testing.T) {
	recipient := "colleague@example.test"
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{
		recipient: {ID: uuid.New(), MaxMessageBytes: 1_000_000},
	}}
	messages := &recordingMessageRepository{}
	inbound := NewProcessInboundEmailUseCase(
		mailboxes, &stubBlobStorage{ref: port.BlobRef{ID: uuid.New(), SHA256: "s", SizeBytes: 4}}, messages,
	)

	mx := &countingMXResolver{}
	repo := &fakeOutboundRepo{}
	job := newLocalDeliveryJob(recipient)
	repo.jobs = append(repo.jobs, job)

	worker := NewOutboundWorkerUseCase(
		repo, mx,
		&fakeBlobReader{payload: []byte("From: me@example.test\r\nTo: " + recipient + "\r\n\r\nhello\r\n")},
		inbound, mailboxes, "mail.example.test",
	)

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	if mx.lookups != 0 {
		t.Errorf("a local recipient triggered %d MX lookups; delivery left the host", mx.lookups)
	}
	if len(messages.persisted) != 1 {
		t.Fatalf("want the message stored once for the local recipient, got %d", len(messages.persisted))
	}
	if job.Status != entity.OutboundJobStatusDelivered {
		t.Errorf("job status = %q, want delivered", job.Status)
	}
	if job.TlsPolicyUsed != entity.TLSModeLocal {
		t.Errorf("tls policy = %q, want %q - nothing crossed a network",
			job.TlsPolicyUsed, entity.TLSModeLocal)
	}
}

// The shortcut must be narrow: an address this server does not host still goes
// out over SMTP, or local delivery would swallow the internet's mail.
func TestRemoteRecipientStillGoesOutOverSmtp(t *testing.T) {
	mailboxes := &fakeMailboxRepository{byAddress: map[string]port.MailboxRecord{}}
	messages := &recordingMessageRepository{}
	inbound := NewProcessInboundEmailUseCase(
		mailboxes, &stubBlobStorage{ref: port.BlobRef{ID: uuid.New()}}, messages,
	)

	mx := &countingMXResolver{}
	repo := &fakeOutboundRepo{}
	job := newLocalDeliveryJob("stranger@remote.test")
	job.DestinationDomain = "remote.test"
	repo.jobs = append(repo.jobs, job)

	worker := NewOutboundWorkerUseCase(
		repo, mx, &fakeBlobReader{payload: []byte("From: me\r\n\r\nhi\r\n")},
		inbound, mailboxes, "mail.example.test",
	)

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	if mx.lookups == 0 {
		t.Error("a remote recipient was not resolved through MX")
	}
	if len(messages.persisted) != 0 {
		t.Errorf("a remote message was filed into a local mailbox: %+v", messages.persisted)
	}
}
