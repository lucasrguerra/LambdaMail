package postgres

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
)

// End to end over a real database: a report arrives as mail, is parsed into
// the report tables, and is filed into Reports without raising an unread badge.

// The report Google actually sends, with the domain changed.
const liveTlsRptReport = `{"organization-name":"Google Inc.",` +
	`"date-range":{"start-datetime":"2026-08-19T00:00:00Z","end-datetime":"2026-08-19T23:59:59Z"},` +
	`"contact-info":"smtp-tls-reporting@google.com","report-id":"2026-08-19T00:00:00Z_example.test",` +
	`"policies":[{"policy":{"policy-type":"no-policy-found","policy-domain":"example.test"},` +
	`"summary":{"total-successful-session-count":2,"total-failure-session-count":0}}]}`

func reportMail(t *testing.T, filename string, content []byte) []byte {
	t.Helper()
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	if _, err := gz.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(gzBuf.Bytes())

	var b bytes.Buffer
	b.WriteString("From: smtp-tls-reporting@google.com\r\n")
	b.WriteString("Subject: Report Domain: example.test\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"sep\"\r\n\r\n")
	b.WriteString("--sep\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\nReport attached.\r\n")
	b.WriteString("--sep\r\n")
	b.WriteString("Content-Type: application/gzip; name=\"" + filename + "\"\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"" + filename + "\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(encoded + "\r\n")
	b.WriteString("--sep--\r\n")
	return b.Bytes()
}

func TestDeliveredTlsRptReportIsIngestedAndFiled(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	mailboxID := seedMailbox(t, pool)

	// The Reports folder migration 0008 adds for every mailbox.
	if _, err := pool.Exec(ctx,
		`INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1, 'Reports', NULL)`,
		uuid.MustParse(mailboxID)); err != nil {
		t.Fatalf("seed Reports folder: %v", err)
	}

	reportRepo := NewReportRepository(pool)
	inbound := usecase.NewProcessInboundEmailUseCase(
		nil, &dbBlobStore{pool: pool, dir: t.TempDir()}, NewInboundMessageRepository(pool))
	inbound.SetReportIngestor(
		usecase.NewIngestDeliveredReportsUseCase(usecase.NewIngestReportsUseCase(reportRepo)))

	reportID := "delivered-" + uuid.NewString()
	payload := reportMail(t,
		"google.com!example.test!1787097600!1787183999!001.json.gz",
		[]byte(strings.Replace(liveTlsRptReport,
			"2026-08-19T00:00:00Z_example.test", reportID, 1)))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM tls_rpt_reports WHERE report_id = $1`, reportID)
	})

	if err := inbound.Handle(ctx, usecase.ProcessInboundEmailInput{
		Sender:             "smtp-tls-reporting@google.com",
		Recipients:         []port.MailboxRecord{{ID: uuid.MustParse(mailboxID)}},
		RecipientAddresses: []string{"tlsrpt@example.test"},
		Body:               bytes.NewReader(payload),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// The report reached the table the admin console reads.
	var stored int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM tls_rpt_reports WHERE report_id = $1`, reportID).Scan(&stored); err != nil {
		t.Fatalf("count stored reports: %v", err)
	}
	if stored != 1 {
		t.Errorf("the report was not stored: %d rows", stored)
	}

	// And the message went to Reports, silently.
	webmail := NewWebmailRepository(pool)
	folders, err := webmail.ListFolders(ctx, mailboxID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	for _, f := range folders {
		switch f.Name {
		case "Reports":
			if f.TotalCount != 1 {
				t.Errorf("Reports holds %d messages, want 1", f.TotalCount)
			}
			if f.UnreadCount != 0 {
				t.Errorf("Reports shows %d unread; a machine-read report raises no badge", f.UnreadCount)
			}
		case "INBOX":
			if f.TotalCount != 0 {
				t.Errorf("the report landed in the inbox: %d messages", f.TotalCount)
			}
		}
	}
}

// dbBlobStore writes a real message_blobs row so the delivery's foreign keys
// hold, without needing the disk storage driver in a database test.
type dbBlobStore struct {
	pool *pgxpool.Pool
	// dir is where the bytes actually land, so the real disk reader can read
	// them back. Pointing storage_path at a file that does not exist would
	// leave the read path untested.
	dir string
}

func (s *dbBlobStore) Store(ctx context.Context, r io.Reader) (port.BlobRef, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return port.BlobRef{}, err
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	// Deduplicated on the digest, as the real storage drivers are: identical
	// bytes are one blob, and a second delivery of the same report reuses it.
	id := uuid.New()
	path := filepath.Join(s.dir, id.String()+".eml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return port.BlobRef{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes, ref_count)
		VALUES ($1, $2, 'local', $3, $4, 0)
		ON CONFLICT (content_sha256) DO UPDATE SET storage_path = EXCLUDED.storage_path
		RETURNING id`,
		id, digest, path, len(body)).Scan(&id); err != nil {
		return port.BlobRef{}, err
	}
	return port.BlobRef{ID: id, SHA256: digest, SizeBytes: int64(len(body))}, nil
}

// A reporter that retries - or an SMTP retry after a slow commit - delivers
// the same report twice. Automatic ingestion makes that routine, where before
// reports were only ever written by hand through the API, so the same report
// arriving again must not double every number the console shows.
func TestIngestingTheSameReportTwiceStoresItOnce(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewReportRepository(pool)

	reportID := "retry-" + uuid.NewString()
	report := func() *entity.TlsRptReport {
		return &entity.TlsRptReport{
			OrganizationName: "Google Inc.",
			ReportID:         reportID,
			Domain:           "example.test",
			DateRangeBegin:   time.Now().Add(-24 * time.Hour),
			DateRangeEnd:     time.Now(),
			Policies: []entity.TlsRptPolicy{
				{PolicyType: "no-policy-found", SuccessCount: 2, FailureCount: 0},
			},
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM tls_rpt_reports WHERE report_id = $1`, reportID)
	})

	if err := repo.SaveTlsRptReport(ctx, report()); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := repo.SaveTlsRptReport(ctx, report()); err != nil {
		t.Fatalf("a redelivered report must not error: %v", err)
	}

	var headers, policies int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM tls_rpt_reports WHERE report_id = $1`, reportID).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if headers != 1 {
		t.Errorf("the same report is stored %d times", headers)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM tls_rpt_report_policies p
		  JOIN tls_rpt_reports r ON r.id = p.report_id
		 WHERE r.report_id = $1`, reportID).Scan(&policies); err != nil {
		t.Fatal(err)
	}
	if policies != 1 {
		t.Errorf("the report's policies are stored %d times", policies)
	}
}

// The same, for DMARC: its unique key includes domain_id, which this path
// never fills, and in Postgres a NULL never conflicts - so the key it had did
// not in fact prevent a duplicate.
func TestIngestingTheSameDmarcReportTwiceStoresItOnce(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewReportRepository(pool)

	reportID := "retry-" + uuid.NewString()
	report := func() *entity.DmarcReport {
		return &entity.DmarcReport{
			OrgName:        "google.com",
			ReportID:       reportID,
			Domain:         "example.test",
			DateRangeBegin: time.Now().Add(-24 * time.Hour),
			DateRangeEnd:   time.Now(),
			Records: []entity.DmarcRecord{
				{SourceIP: "203.0.113.10", Count: 3, Disposition: "none", DKIMResult: "pass", SPFResult: "pass"},
			},
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM dmarc_reports WHERE report_id = $1`, reportID)
	})

	if err := repo.SaveDmarcReport(ctx, report()); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := repo.SaveDmarcReport(ctx, report()); err != nil {
		t.Fatalf("a redelivered report must not error: %v", err)
	}

	var headers int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM dmarc_reports WHERE report_id = $1`, reportID).Scan(&headers); err != nil {
		t.Fatal(err)
	}
	if headers != 1 {
		t.Errorf("the same DMARC report is stored %d times", headers)
	}
}

// The backfill, over a real database: a report that was delivered to the
// inbox before ingestion existed is parsed after the fact and filed away.
func TestBackfillParsesAReportAlreadySittingInTheInbox(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	mailboxID := seedMailbox(t, pool)

	if _, err := pool.Exec(ctx,
		`INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1, 'Reports', NULL)`,
		uuid.MustParse(mailboxID)); err != nil {
		t.Fatalf("seed Reports folder: %v", err)
	}

	// Delivered the old way: straight into the inbox, unread, never parsed.
	reportID := "backfill-" + uuid.NewString()
	body := `{"organization-name":"Google Inc.",` +
		`"date-range":{"start-datetime":"2026-08-19T00:00:00Z","end-datetime":"2026-08-19T23:59:59Z"},` +
		`"report-id":"` + reportID + `",` +
		`"policies":[{"policy":{"policy-type":"sts","policy-domain":"example.test"},` +
		`"summary":{"total-successful-session-count":5,"total-failure-session-count":1}}]}`
	payload := reportMail(t, "google.com!example.test!1!2!001.json.gz", []byte(body))

	blobs := &dbBlobStore{pool: pool, dir: t.TempDir()}
	blob, err := blobs.Store(ctx, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("store blob: %v", err)
	}
	uids, err := NewInboundMessageRepository(pool).PersistAll(ctx, []port.PersistInboundMessageInput{{
		MailboxID:        uuid.MustParse(mailboxID),
		Blob:             blob,
		SenderAddress:    "smtp-tls-reporting@google.com",
		RecipientAddress: "tlsrpt@example.test",
		TargetFolderName: "INBOX",
		Subject:          "Report Domain: example.test",
	}})
	if err != nil || len(uids) != 1 {
		t.Fatalf("seed delivered report: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM tls_rpt_reports WHERE report_id = $1`, reportID)
	})

	store := NewDeliveredReportStore(pool)
	uc := usecase.NewBackfillReportsUseCase(store, diskstorage.NewLocalDiskBlobReader(pool),
		usecase.NewIngestDeliveredReportsUseCase(
			usecase.NewIngestReportsUseCase(NewReportRepository(pool))))

	summary, err := uc.Run(ctx, 0)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if summary.Ingested < 1 {
		t.Fatalf("the backlogged report was not ingested: %+v", summary)
	}

	var stored int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM tls_rpt_reports WHERE report_id = $1`, reportID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Errorf("the report reached the table %d times, want 1", stored)
	}

	// And it left the inbox.
	webmail := NewWebmailRepository(pool)
	folders, _ := webmail.ListFolders(ctx, mailboxID)
	for _, f := range folders {
		if f.Name == "INBOX" && f.TotalCount != 0 {
			t.Errorf("the backfilled report is still in the inbox: %d messages", f.TotalCount)
		}
		if f.Name == "Reports" {
			if f.TotalCount != 1 {
				t.Errorf("Reports holds %d messages, want 1", f.TotalCount)
			}
			if f.UnreadCount != 0 {
				t.Errorf("the backfilled report raised an unread badge: %d", f.UnreadCount)
			}
		}
	}

	// Running it again finds nothing left to do.
	second, err := uc.Run(ctx, 0)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Ingested != 0 {
		t.Errorf("a second pass re-ingested %d reports; it should find none", second.Ingested)
	}
}
