package e2e

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
)

type staticMXResolver struct {
	addr string
}

func (s *staticMXResolver) LookupMX(_ context.Context, _ string) ([]string, error) {
	return []string{s.addr}, nil
}

func TestOutboundQueueEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54339).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54339/postgres?sslmode=disable"
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	root := repoRoot(t)
	migrations := []string{
		"0001_init_schema.up.sql",
		"0002_add_is_system_to_aliases.up.sql",
		"0003_create_report_tables.up.sql",
	}

	for _, m := range migrations {
		sql, err := os.ReadFile(filepath.Join(root, "migrations", m))
		if err != nil {
			t.Fatalf("read migration %s: %v", m, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply migration %s: %v", m, err)
		}
	}

	// Seed domain, sender mailbox, INBOX
	domainID := uuid.New()
	mailboxID := uuid.New()
	inboxID := uuid.New()

	pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'outbound.test', 'outbound.test')`, domainID)
	pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'sender', 'sender@outbound.test', 'hash')`, mailboxID, domainID)
	pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`, inboxID, mailboxID)

	mailboxRepo := postgres.NewMailboxRepository(pool)
	messageRepo := postgres.NewInboundMessageRepository(pool)
	outboundRepo := postgres.NewOutboundRepository(pool)
	spool := t.TempDir()
	blobStorage := diskstorage.NewLocalDiskBlobStorage(pool, spool)
	blobReader := diskstorage.NewLocalDiskBlobReader(pool)

	inboundUC := appusecase.NewProcessInboundEmailUseCase(mailboxRepo, blobStorage, messageRepo)

	// Create sample message blob
	blobRef, err := blobStorage.Store(ctx, strings.NewReader("From: sender@outbound.test\r\nTo: rcpt@remote.test\r\n\r\nOutbound Payload"))
	if err != nil {
		t.Fatalf("store blob: %v", err)
	}

	// Mock remote SMTP server listening on TCP
	mockSMTPLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock smtp: %v", err)
	}
	defer mockSMTPLn.Close()

	go func() {
		for {
			conn, err := mockSMTPLn.Accept()
			if err != nil {
				return
			}
			go handleMockSMTPSession(conn)
		}
	}()

	mxResolver := &staticMXResolver{addr: mockSMTPLn.Addr().String()}
	worker := appusecase.NewOutboundWorkerUseCase(outboundRepo, mxResolver, blobReader, inboundUC, mailboxRepo, "mail.outbound.test")

	// 1. Enqueue Job & Deliver
	job := &entity.OutboundJob{
		MailboxID:         &mailboxID,
		BlobID:            blobRef.ID,
		EnvelopeFrom:      "sender@outbound.test",
		EnvelopeTo:        "rcpt@remote.test",
		DestinationDomain: "remote.test",
		Status:            entity.OutboundJobStatusQueued,
		Attempt:           0,
		NextAttemptAt:     time.Now().Add(-1 * time.Minute),
		ExpiresAt:         time.Now().Add(5 * 24 * time.Hour),
	}

	if err := outboundRepo.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	processed, err := worker.ProcessBatch(ctx, "worker1", 10)
	if err != nil || processed != 1 {
		t.Fatalf("ProcessBatch delivery: processed=%d, err=%v", processed, err)
	}

	var status, lastErr string
	pool.QueryRow(ctx, `SELECT status, COALESCE(last_error, '') FROM outbound_jobs WHERE id = $1`, job.ID).Scan(&status, &lastErr)
	if status != "DELIVERED" {
		t.Errorf("expected DELIVERED status, got %s (last_error: %s)", status, lastErr)
	}

	// 2. Permanent Failure -> Bounce DSN delivered to sender's INBOX
	failedJob := &entity.OutboundJob{
		MailboxID:         &mailboxID,
		BlobID:            blobRef.ID,
		EnvelopeFrom:      "sender@outbound.test",
		EnvelopeTo:        "invalid@remote.test",
		DestinationDomain: "unreachable.test",
		Status:            entity.OutboundJobStatusQueued,
		Attempt:           5,
		NextAttemptAt:     time.Now().Add(-1 * time.Minute),
		ExpiresAt:         time.Now().Add(-1 * time.Hour), // Expired
	}
	outboundRepo.Enqueue(ctx, failedJob)

	// Unreachable MX resolver to trigger permanent failure on expiration
	unreachableWorker := appusecase.NewOutboundWorkerUseCase(outboundRepo, &staticMXResolver{addr: "127.0.0.1:59999"}, blobReader, inboundUC, mailboxRepo, "mail.outbound.test")
	unreachableWorker.ProcessBatch(ctx, "worker1", 10)

	var failedStatus string
	pool.QueryRow(ctx, `SELECT status FROM outbound_jobs WHERE id = $1`, failedJob.ID).Scan(&failedStatus)
	if failedStatus != "BOUNCED" {
		t.Errorf("expected BOUNCED status, got %s", failedStatus)
	}

	// Verify DSN bounce email delivered to sender's INBOX
	var inboxCount int
	pool.QueryRow(ctx, `SELECT total_count FROM folders WHERE id = $1`, inboxID).Scan(&inboxCount)
	if inboxCount != 1 {
		t.Errorf("expected 1 DSN bounce message in INBOX, got %d", inboxCount)
	}
}

func handleMockSMTPSession(conn net.Conn) {
	defer conn.Close()
	conn.Write([]byte("220 mock.smtp.test ESMTP\r\n"))
	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		if strings.HasPrefix(cmd, "EHLO") || strings.HasPrefix(cmd, "HELO") {
			conn.Write([]byte("250-mock.smtp.test\r\n250 HELP\r\n"))
		} else if strings.HasPrefix(cmd, "MAIL FROM:") {
			conn.Write([]byte("250 2.1.0 Ok\r\n"))
		} else if strings.HasPrefix(cmd, "RCPT TO:") {
			conn.Write([]byte("250 2.1.5 Ok\r\n"))
		} else if strings.HasPrefix(cmd, "DATA") {
			conn.Write([]byte("354 End data with <CR><LF>.<CR><LF>\r\n"))
			// Read until dot
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil || strings.TrimSpace(dataLine) == "." {
					break
				}
			}
			conn.Write([]byte("250 2.0.0 Ok queued\r\n"))
		} else if strings.HasPrefix(cmd, "QUIT") {
			conn.Write([]byte("221 2.0.0 Bye\r\n"))
			return
		} else {
			conn.Write([]byte("250 2.0.0 Ok\r\n"))
		}
	}
}
