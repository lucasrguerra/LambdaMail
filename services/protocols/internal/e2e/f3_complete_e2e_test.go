package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/infrastructure/clamav"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	"lambdamail/protocols/internal/infrastructure/rspamd"
	httppresentation "lambdamail/protocols/internal/presentation/http"
	smtppresentation "lambdamail/protocols/internal/presentation/smtp"
	gosmtp "github.com/emersion/go-smtp"
)

type fakeDnsProvider struct {
	records map[string]entity.DnsRecord
}

func newFakeDnsProvider() *fakeDnsProvider {
	return &fakeDnsProvider{records: make(map[string]entity.DnsRecord)}
}

func (f *fakeDnsProvider) GetZoneID(_ context.Context, domainName string) (string, error) {
	return fmt.Sprintf("zone_%s", domainName), nil
}

func (f *fakeDnsProvider) ListRecords(_ context.Context, _ string) ([]entity.DnsRecord, error) {
	var out []entity.DnsRecord
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeDnsProvider) CreateRecord(_ context.Context, _ string, record entity.DnsRecord) error {
	key := fmt.Sprintf("%s:%s", strings.ToUpper(record.Type), strings.ToLower(record.Name))
	record.ID = fmt.Sprintf("id_%s", key)
	f.records[key] = record
	return nil
}

func (f *fakeDnsProvider) UpdateRecord(_ context.Context, _ string, record entity.DnsRecord) error {
	key := fmt.Sprintf("%s:%s", strings.ToUpper(record.Type), strings.ToLower(record.Name))
	f.records[key] = record
	return nil
}

func (f *fakeDnsProvider) DeleteRecord(_ context.Context, _ string, recordID string) error {
	for k, r := range f.records {
		if r.ID == recordID {
			delete(f.records, k)
			break
		}
	}
	return nil
}

func TestPhase3CompleteEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Phase 3 complete E2E test in -short mode")
	}
	ctx := context.Background()

	// Initialize embedded PostgreSQL
	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54340).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54340/postgres?sslmode=disable"
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	// Apply Database Migrations
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

	// -------------------------------------------------------------------
	// 1. Sub-project 1: Cloudflare DNS Automation Engine & System Aliases
	// -------------------------------------------------------------------
	domainID := uuid.New()
	mailboxID := uuid.New()
	inboxID := uuid.New()
	junkID := uuid.New()

	_, err = pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'f3e2e.test', 'f3e2e.test')`, domainID)
	if err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'alice', 'alice@f3e2e.test', 'hash')`, mailboxID, domainID)
	if err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`, inboxID, mailboxID)
	if err != nil {
		t.Fatalf("insert inbox: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'Junk', 'junk')`, junkID, mailboxID)
	if err != nil {
		t.Fatalf("insert junk folder: %v", err)
	}

	dnsProvider := newFakeDnsProvider()
	aliasRepo := postgres.NewAliasRepository(pool)
	syncDnsUC := appusecase.NewSyncDnsRecordsUseCase(dnsProvider, aliasRepo)

	dnsOut, err := syncDnsUC.Execute(ctx, appusecase.SyncDnsRecordsInput{
		DomainName:        "f3e2e.test",
		MailHost:          "mail.f3e2e.test",
		ServerIPv4:        "192.0.2.1",
		ServerIPv6:        "2001:db8::1",
		RsaDkimPubKey:     "rsaKey",
		EdDkimPubKey:      "edKey",
		TlsaHash:          "hash123",
		DaneEnabled:       true,
		AdminEmailAddress: "admin@f3e2e.test",
	})
	if err != nil {
		t.Fatalf("sync dns records: %v", err)
	}
	if dnsOut.CreatedCount != 13 {
		t.Fatalf("expected 13 created DNS records, got %d", dnsOut.CreatedCount)
	}

	// Verify system aliases created
	var aliasCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM aliases WHERE domain_id = $1 AND is_system = true`, domainID).Scan(&aliasCount)
	if aliasCount != 4 {
		t.Errorf("expected 4 system aliases (postmaster, abuse, dmarc, tlsrpt), got %d", aliasCount)
	}

	// -------------------------------------------------------------------
	// 2. Sub-project 2: MTA-STS, TLS-RPT & Autoconfig HTTP Service
	// -------------------------------------------------------------------
	reportRepo := postgres.NewReportRepository(pool)
	reportUC := appusecase.NewIngestReportsUseCase(reportRepo)
	httpRouter := httppresentation.NewRouter(reportUC, func() error { return pool.Ping(ctx) })

	// Test MTA-STS endpoint
	reqSts := httptest.NewRequest("GET", "/.well-known/mta-sts.txt", nil)
	reqSts.Host = "mta-sts.f3e2e.test"
	recSts := httptest.NewRecorder()
	httpRouter.ServeHTTP(recSts, reqSts)
	if recSts.Code != http.StatusOK || !strings.Contains(recSts.Body.String(), "mode: enforce") {
		t.Errorf("MTA-STS endpoint error: code=%d body=%s", recSts.Code, recSts.Body.String())
	}

	// Test Autoconfig endpoint
	reqAuto := httptest.NewRequest("GET", "/.well-known/autoconfig/mail/config-v1.1.xml", nil)
	reqAuto.Host = "autoconfig.f3e2e.test"
	recAuto := httptest.NewRecorder()
	httpRouter.ServeHTTP(recAuto, reqAuto)
	if recAuto.Code != http.StatusOK {
		t.Errorf("Autoconfig endpoint error: code=%d", recAuto.Code)
	}

	// Test TLS-RPT ingestion endpoint
	tlsRptBody := `{
		"organization-name": "TestOrg",
		"date-range": {"start-datetime": "2026-07-25T00:00:00Z", "end-datetime": "2026-07-25T23:59:59Z"},
		"contact-info": "admin@f3e2e.test",
		"report-id": "rpt123",
		"policies": [{"policy": {"policy-type": "sts", "policy-string": ["version: STSv1"], "policy-domain": "f3e2e.test"}, "summary": {"total-successful-session-count": 10, "total-failure-session-count": 0}}]
	}`
	reqRpt := httptest.NewRequest("POST", "/api/v1/reports/tlsrpt", strings.NewReader(tlsRptBody))
	reqRpt.Header.Set("Content-Type", "application/tlsrpt+json")
	recRpt := httptest.NewRecorder()
	httpRouter.ServeHTTP(recRpt, reqRpt)
	if recRpt.Code != http.StatusOK {
		t.Errorf("TLS-RPT report ingestion error: code=%d", recRpt.Code)
	}

	var tlsRptCount int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM tls_rpt_reports`).Scan(&tlsRptCount)
	if tlsRptCount != 1 {
		t.Errorf("expected 1 stored TLS report, got %d", tlsRptCount)
	}

	// -------------------------------------------------------------------
	// 3. Sub-project 3: Antispam & Antivirus Scanning Pipeline
	// -------------------------------------------------------------------
	mailboxRepo := postgres.NewMailboxRepository(pool)
	spool := t.TempDir()
	blobStorage := diskstorage.NewLocalDiskBlobStorage(pool, spool)
	blobReader := diskstorage.NewLocalDiskBlobReader(pool)
	inboundMessageRepo := postgres.NewInboundMessageRepository(pool)

	processInboundUC := appusecase.NewProcessInboundEmailUseCase(mailboxRepo, blobStorage, inboundMessageRepo)

	var clamavInfected bool
	clamLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock clamav: %v", err)
	}
	defer clamLn.Close()

	go func() {
		for {
			conn, err := clamLn.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			for {
				_, err := conn.Read(buf)
				if err != nil {
					break
				}
			}
			if clamavInfected {
				conn.Write([]byte("stream: EICAR-Test-Signature FOUND\x00"))
			} else {
				conn.Write([]byte("stream: OK\x00"))
			}
			conn.Close()
		}
	}()

	var rspamdAction string = "no action"
	rspamdServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"action":         rspamdAction,
			"score":          5.0,
			"required_score": 15.0,
			"symbols":        map[string]any{},
		})
	}))
	defer rspamdServer.Close()

	clamAdapter := clamav.NewClamAVAdapter(clamLn.Addr().String())
	rspamdAdapter := rspamd.NewRspamdAdapter(rspamdServer.URL)
	pipeline := appusecase.NewScanningPipeline(clamAdapter, rspamdAdapter)
	processInboundUC.SetScanner(pipeline)

	// SMTP Presentation Server
	smtpBackend := smtppresentation.NewBackend(processInboundUC)
	smtpServer := gosmtp.NewServer(smtpBackend)
	smtpServer.Domain = "mail.f3e2e.test"

	smtpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp: %v", err)
	}
	defer smtpLn.Close()

	go smtpServer.Serve(smtpLn)
	defer smtpServer.Close()

	// 3a. Virus email -> Rejected
	clamavInfected = true
	err = sendTestEmail(smtpLn.Addr().String(), "external@remote.test", "alice@f3e2e.test", "Subject: Test\r\n\r\nEICAR-VIRUS-FOUND")
	if err == nil || !strings.Contains(err.Error(), "554 5.7.1 Virus detected") {
		t.Errorf("expected 554 virus rejection, got %v", err)
	}

	// 3b. Spam reject email -> Rejected
	clamavInfected = false
	rspamdAction = "reject"
	err = sendTestEmail(smtpLn.Addr().String(), "external@remote.test", "alice@f3e2e.test", "Subject: Test\r\n\r\nSPAM-REJECT-BODY")
	if err == nil || !strings.Contains(err.Error(), "554 5.7.1 Spam threshold exceeded") {
		t.Errorf("expected 554 spam rejection, got %v", err)
	}

	// 3c. Spam junk email -> Accepted & auto-filed to Junk folder
	clamavInfected = false
	rspamdAction = "add header"
	err = sendTestEmail(smtpLn.Addr().String(), "external@remote.test", "alice@f3e2e.test", "Subject: Test Junk\r\n\r\nSPAM-JUNK-BODY")
	if err != nil {
		t.Fatalf("send junk email: %v", err)
	}

	var junkCount int
	pool.QueryRow(ctx, `SELECT total_count FROM folders WHERE id = $1`, junkID).Scan(&junkCount)
	if junkCount != 1 {
		t.Errorf("expected 1 message in Junk folder, got %d", junkCount)
	}

	// -------------------------------------------------------------------
	// 4. Sub-project 4: Outbound Queue, Retry Backoff Engine & DSN Bounces
	// -------------------------------------------------------------------
	clamavInfected = false
	rspamdAction = "no action"
	outboundRepo := postgres.NewOutboundRepository(pool)

	// Mock Remote SMTP Server
	mockRemoteSMTPLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock remote smtp: %v", err)
	}
	defer mockRemoteSMTPLn.Close()

	go func() {
		for {
			conn, err := mockRemoteSMTPLn.Accept()
			if err != nil {
				return
			}
			go handleMockSMTPSession(conn)
		}
	}()

	mxResolver := &staticMXResolver{addr: mockRemoteSMTPLn.Addr().String()}
	outboundWorker := appusecase.NewOutboundWorkerUseCase(outboundRepo, mxResolver, blobReader, processInboundUC, mailboxRepo, "mail.f3e2e.test")

	// Create sample outbound message blob
	outboundBlob, err := blobStorage.Store(ctx, strings.NewReader("From: alice@f3e2e.test\r\nTo: bob@remote.test\r\n\r\nHello Bob"))
	if err != nil {
		t.Fatalf("store outbound blob: %v", err)
	}

	// Enqueue & Deliver outbound job
	outJob := &entity.OutboundJob{
		MailboxID:         &mailboxID,
		BlobID:            outboundBlob.ID,
		EnvelopeFrom:      "alice@f3e2e.test",
		EnvelopeTo:        "bob@remote.test",
		DestinationDomain: "remote.test",
		Status:            entity.OutboundJobStatusQueued,
		Attempt:           0,
		NextAttemptAt:     time.Now().Add(-1 * time.Minute),
		ExpiresAt:         time.Now().Add(5 * 24 * time.Hour),
	}
	if err := outboundRepo.Enqueue(ctx, outJob); err != nil {
		t.Fatalf("enqueue outbound job: %v", err)
	}

	processed, err := outboundWorker.ProcessBatch(ctx, "worker1", 10)
	if err != nil || processed != 1 {
		t.Fatalf("process outbound batch: processed=%d, err=%v", processed, err)
	}

	var outStatus string
	pool.QueryRow(ctx, `SELECT status FROM outbound_jobs WHERE id = $1`, outJob.ID).Scan(&outStatus)
	if outStatus != "DELIVERED" {
		t.Errorf("expected outbound job DELIVERED, got %s", outStatus)
	}

	// Permanent Failure -> Expiration triggers DSN bounce delivered to alice's INBOX
	failedJob := &entity.OutboundJob{
		MailboxID:         &mailboxID,
		BlobID:            outboundBlob.ID,
		EnvelopeFrom:      "alice@f3e2e.test",
		EnvelopeTo:        "dead@remote.test",
		DestinationDomain: "unreachable.test",
		Status:            entity.OutboundJobStatusQueued,
		Attempt:           5,
		NextAttemptAt:     time.Now().Add(-1 * time.Minute),
		ExpiresAt:         time.Now().Add(-1 * time.Hour),
	}
	outboundRepo.Enqueue(ctx, failedJob)

	unreachableWorker := appusecase.NewOutboundWorkerUseCase(outboundRepo, &staticMXResolver{addr: "127.0.0.1:59999"}, blobReader, processInboundUC, mailboxRepo, "mail.f3e2e.test")
	unreachableWorker.ProcessBatch(ctx, "worker1", 10)

	var failedStatus string
	pool.QueryRow(ctx, `SELECT status FROM outbound_jobs WHERE id = $1`, failedJob.ID).Scan(&failedStatus)
	if failedStatus != "BOUNCED" {
		t.Errorf("expected failed job BOUNCED, got %s", failedStatus)
	}

	// Verify DSN bounce delivered to alice's INBOX
	var inboxCount int
	pool.QueryRow(ctx, `SELECT total_count FROM folders WHERE id = $1`, inboxID).Scan(&inboxCount)
	if inboxCount != 1 {
		t.Errorf("expected 1 DSN bounce message in INBOX, got %d", inboxCount)
	}
}

func sendTestEmail(addr string, from string, to string, msg string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	reader.ReadString('\n') // Greeting

	fmt.Fprintf(conn, "EHLO client.test\r\n")
	for {
		line, _ := reader.ReadString('\n')
		if strings.HasPrefix(line, "250 ") {
			break
		}
	}

	fmt.Fprintf(conn, "MAIL FROM:<%s>\r\n", from)
	line, _ := reader.ReadString('\n')
	if !strings.HasPrefix(line, "250") {
		return fmt.Errorf("MAIL FROM error: %s", line)
	}

	fmt.Fprintf(conn, "RCPT TO:<%s>\r\n", to)
	line, _ = reader.ReadString('\n')
	if !strings.HasPrefix(line, "250") {
		return fmt.Errorf("RCPT TO error: %s", line)
	}

	fmt.Fprintf(conn, "DATA\r\n")
	line, _ = reader.ReadString('\n')
	if !strings.HasPrefix(line, "354") {
		return fmt.Errorf("DATA error: %s", line)
	}

	fmt.Fprintf(conn, "%s\r\n.\r\n", msg)
	line, _ = reader.ReadString('\n')
	if !strings.HasPrefix(line, "250") {
		return fmt.Errorf("DATA body error: %s", strings.TrimSpace(line))
	}

	fmt.Fprintf(conn, "QUIT\r\n")
	return nil
}
