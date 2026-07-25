package e2e

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/google/uuid"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/clamav"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	"lambdamail/protocols/internal/infrastructure/rspamd"
	smtppresentation "lambdamail/protocols/internal/presentation/smtp"
)

func TestAntispamAntivirusEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54338).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54338/postgres?sslmode=disable"
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

	// Seed domain, mailbox, INBOX and Junk folders
	domainID := uuid.New()
	mailboxID := uuid.New()
	inboxID := uuid.New()
	junkID := uuid.New()

	pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'scan.test', 'scan.test')`, domainID)
	pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', 'user@scan.test', 'hash')`, mailboxID, domainID)
	pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`, inboxID, mailboxID)
	pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'Junk', 'junk')`, junkID, mailboxID)

	mailboxRepo := postgres.NewMailboxRepository(pool)
	messageRepo := postgres.NewInboundMessageRepository(pool)
	spool := t.TempDir()
	blobStorage := diskstorage.NewLocalDiskBlobStorage(pool, spool)

	processInboundUC := appusecase.NewProcessInboundEmailUseCase(mailboxRepo, blobStorage, messageRepo)

	// Mock ClamAV server state
	var clamavInfected bool

	clamavLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock clamav: %v", err)
	}
	defer clamavLn.Close()

	go func() {
		for {
			conn, err := clamavLn.Accept()
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

	// Mock Rspamd server state
	var rspamdAction string = "no action"

	rspamdTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"action":         rspamdAction,
			"score":          5.0,
			"required_score": 15.0,
			"symbols":        map[string]interface{}{},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer rspamdTS.Close()

	clamavAdapter := clamav.NewClamAVAdapter(clamavLn.Addr().String())
	rspamdAdapter := rspamd.NewRspamdAdapter(rspamdTS.URL)

	pipeline := appusecase.NewScanningPipeline(clamavAdapter, rspamdAdapter)
	processInboundUC.SetScanner(pipeline)

	smtpServer := gosmtp.NewServer(smtppresentation.NewBackend(processInboundUC))
	smtpServer.Addr = "127.0.0.1:25026"
	smtpServer.Domain = "scan.test"
	smtpServer.AllowInsecureAuth = true

	go smtpServer.ListenAndServe()
	defer smtpServer.Close()

	time.Sleep(100 * time.Millisecond)

	// 1. Clean message -> delivered to INBOX
	clamavInfected = false
	rspamdAction = "no action"
	err = sendTestSMTPMessage("127.0.0.1:25026", "sender@external.test", "user@scan.test", "Clean Email Body")
	if err != nil {
		t.Fatalf("clean email delivery failed: %v", err)
	}

	var inboxCount int
	pool.QueryRow(ctx, `SELECT total_count FROM folders WHERE id = $1`, inboxID).Scan(&inboxCount)
	if inboxCount != 1 {
		t.Errorf("expected 1 message in INBOX, got %d", inboxCount)
	}

	// 2. Virus message -> rejected with 554 5.7.1
	clamavInfected = true
	err = sendTestSMTPMessage("127.0.0.1:25026", "sender@external.test", "user@scan.test", "X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")
	if err == nil || !strings.Contains(err.Error(), "554 5.7.1 Virus detected") {
		t.Fatalf("expected 554 virus rejection, got: %v", err)
	}

	// 3. Spam message with action=add header -> delivered to Junk folder
	clamavInfected = false
	rspamdAction = "add header"
	err = sendTestSMTPMessage("127.0.0.1:25026", "sender@external.test", "user@scan.test", "Spammy Email Body")
	if err != nil {
		t.Fatalf("junk email delivery failed: %v", err)
	}

	var junkCount int
	pool.QueryRow(ctx, `SELECT total_count FROM folders WHERE id = $1`, junkID).Scan(&junkCount)
	if junkCount != 1 {
		t.Errorf("expected 1 message in Junk folder, got %d", junkCount)
	}
}

func sendTestSMTPMessage(addr, sender, rcpt, body string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	buf := make([]byte, 1024)
	conn.Read(buf) // greeting

	conn.Write([]byte("HELO test.sender\r\n"))
	conn.Read(buf)

	conn.Write([]byte("MAIL FROM:<" + sender + ">\r\n"))
	conn.Read(buf)

	conn.Write([]byte("RCPT TO:<" + rcpt + ">\r\n"))
	conn.Read(buf)

	conn.Write([]byte("DATA\r\n"))
	conn.Read(buf)

	msg := "From: " + sender + "\r\nTo: " + rcpt + "\r\nSubject: Test\r\n\r\n" + body + "\r\n.\r\n"
	conn.Write([]byte(msg))

	n, _ := conn.Read(buf)
	respStr := string(buf[:n])

	if !strings.HasPrefix(respStr, "250") {
		return fmtError(respStr)
	}
	return nil
}

func fmtError(msg string) error {
	return net.UnknownNetworkError(strings.TrimSpace(msg))
}
