package usecase

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

// startFakeMX serves one SMTP conversation, answering MAIL FROM with the given
// reply so the worker can be driven through a real permanent/temporary failure.
func startFakeMX(t *testing.T, mailReply string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(c)
				c.Write([]byte("220 fake.mx ESMTP\r\n"))
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						c.Write([]byte("250 fake.mx\r\n"))
					case strings.HasPrefix(cmd, "MAIL FROM"):
						c.Write([]byte(mailReply + "\r\n"))
					case strings.HasPrefix(cmd, "QUIT"):
						c.Write([]byte("221 Bye\r\n"))
						return
					default:
						c.Write([]byte("250 Ok\r\n"))
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String()
}

func newTestJob() *entity.OutboundJob {
	mbID := uuid.New()
	return &entity.OutboundJob{
		ID:                uuid.New(),
		MailboxID:         &mbID,
		BlobID:            uuid.New(),
		EnvelopeFrom:      "user@domain.test",
		EnvelopeTo:        "rcpt@remote.test",
		DestinationDomain: "remote.test",
		Status:            entity.OutboundJobStatusQueued,
		ExpiresAt:         time.Now().Add(5 * 24 * time.Hour),
	}
}

// PLAN.md section 6.3: a 5xx is a permanent failure and must bounce
// immediately with a DSN, not sit in the queue retrying for five days.
func TestOutboundWorker_PermanentSmtpFailureBouncesImmediately(t *testing.T) {
	addr := startFakeMX(t, "550 5.1.1 User unknown")

	repo := &fakeOutboundRepo{}
	repo.Enqueue(context.Background(), newTestJob())

	worker := NewOutboundWorkerUseCase(
		repo,
		&fakeMXResolver{hosts: []string{addr}},
		&fakeBlobReader{payload: []byte("From: a\r\nTo: b\r\n\r\nTest")},
		nil, nil, "mail.local",
	)

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	got := repo.jobs[0]
	if got.Status != entity.OutboundJobStatusBounced {
		t.Errorf("status = %s, want BOUNCED for a 5xx reply", got.Status)
	}
	if got.LastSmtpCode != "550" {
		t.Errorf("LastSmtpCode = %q, want %q", got.LastSmtpCode, "550")
	}
}

// A 4xx is temporary and must be deferred for a later retry.
func TestOutboundWorker_TemporarySmtpFailureIsDeferred(t *testing.T) {
	addr := startFakeMX(t, "451 4.3.0 Try later")

	repo := &fakeOutboundRepo{}
	repo.Enqueue(context.Background(), newTestJob())

	worker := NewOutboundWorkerUseCase(
		repo,
		&fakeMXResolver{hosts: []string{addr}},
		&fakeBlobReader{payload: []byte("From: a\r\nTo: b\r\n\r\nTest")},
		nil, nil, "mail.local",
	)

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	got := repo.jobs[0]
	if got.Status != entity.OutboundJobStatusDeferred {
		t.Errorf("status = %s, want DEFERRED for a 4xx reply", got.Status)
	}
	if got.LastSmtpCode != "451" {
		t.Errorf("LastSmtpCode = %q, want %q", got.LastSmtpCode, "451")
	}
}

// The DSN is delivered back to the original sender. When that sender address is
// an alias fanning out to several mailboxes, the recipient records and their
// envelope addresses must stay aligned - otherwise the delivery indexes past
// the end of the address list and takes the worker down.
func TestOutboundWorker_DsnToFanOutSenderDeliversToEveryTarget(t *testing.T) {
	addr := startFakeMX(t, "550 5.1.1 User unknown")

	repo := &fakeOutboundRepo{}
	repo.Enqueue(context.Background(), newTestJob())

	// The sender is the local, aliased address; the recipient is remote, which
	// is what makes this a delivery that goes out and then bounces back.
	mailboxes := &fakeAliasFanOutRepository{localAddress: "user@domain.test", targets: []port.MailboxRecord{
		{ID: uuid.New(), QuotaBytes: 1000},
		{ID: uuid.New(), QuotaBytes: 1000},
	}}
	messages := &recordingMessageRepository{}
	inbound := NewProcessInboundEmailUseCase(mailboxes, &stubBlobStorage{ref: port.BlobRef{ID: uuid.New()}}, messages)

	worker := NewOutboundWorkerUseCase(
		repo,
		&fakeMXResolver{hosts: []string{addr}},
		&fakeBlobReader{payload: []byte("From: a\r\nTo: b\r\n\r\nTest")},
		inbound, mailboxes, "mail.local",
	)

	if _, err := worker.ProcessBatch(context.Background(), "w1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	if len(messages.persisted) != 2 {
		t.Fatalf("DSN persisted to %d mailboxes, want 2 (one per fan-out target)", len(messages.persisted))
	}
	for i, p := range messages.persisted {
		if p.RecipientAddress != "user@domain.test" {
			t.Errorf("persisted[%d].RecipientAddress = %q, want the original sender", i, p.RecipientAddress)
		}
	}
}
