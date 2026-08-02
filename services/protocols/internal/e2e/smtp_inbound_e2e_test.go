// Package e2e contains end-to-end validation of the F0/F1 scope declared in
// PLAN.md section 16: a real Postgres (embedded, no Docker required), the real
// migration, the real SMTP server wired exactly as cmd/lambdamail-protocols
// does, and a real SMTP client driving the session.
//
// Run: go test ./internal/e2e/ -v (downloads Postgres binaries on first run).
package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	smtppresentation "lambdamail/protocols/internal/presentation/smtp"
)

const testMessage = "From: sender@remote.example\r\n" +
	"To: postmaster@example.test\r\n" +
	"Subject: e2e smoke\r\n" +
	"\r\n" +
	"F1 inbound validation body.\r\n"

func TestInboundSMTPEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	// --- 1. Real Postgres via embedded binaries ---------------------------
	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54329).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54329/postgres?sslmode=disable"
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// --- 2. Real migration + dev seed ------------------------------------
	root := repoRoot(t)
	for _, f := range []string{
		filepath.Join(root, "migrations", "0001_init_schema.up.sql"),
		filepath.Join(root, "scripts", "dev-seed.sql"),
	} {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}

	// --- 3. Real server, wired exactly like main.go -----------------------
	spool := t.TempDir()
	uc := usecase.NewProcessInboundEmailUseCase(
		postgres.NewMailboxRepository(pool),
		diskstorage.NewLocalDiskBlobStorage(pool, spool),
		postgres.NewInboundMessageRepository(pool),
	)
	server := gosmtp.NewServer(smtppresentation.NewBackend(uc))
	server.Domain = "mail.example.test"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go server.Serve(ln)
	defer server.Close()
	addr := ln.Addr().String()

	// --- 4. Unknown recipient must be rejected in-session (550) -----------
	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.Mail("sender@remote.example"); err != nil {
		t.Fatalf("MAIL FROM: %v", err)
	}
	err = c.Rcpt("nobody@example.test")
	if err == nil {
		t.Fatal("RCPT for unknown user succeeded; PLAN section 6.1 requires 550 5.1.1")
	}
	if !strings.Contains(err.Error(), "550") {
		t.Errorf("unknown RCPT error = %q, want SMTP 550", err)
	}
	c.Close()

	// --- 5. Valid delivery ------------------------------------------------
	deliver := func() {
		t.Helper()
		if err := smtp.SendMail(addr, nil, "sender@remote.example",
			[]string{"postmaster@example.test"}, []byte(testMessage)); err != nil {
			t.Fatalf("SendMail: %v", err)
		}
	}
	deliver()

	// --- 6. Durability assertions (PLAN sections 6.1 and 9) ---------------
	var uid int64
	var blobPath string
	var sizeBytes int64
	err = pool.QueryRow(ctx, `
		SELECT m.uid, b.storage_path, b.size_bytes
		FROM email_messages m JOIN message_blobs b ON b.id = m.blob_id
		WHERE m.sender_address = 'sender@remote.example'
		ORDER BY m.uid LIMIT 1
	`).Scan(&uid, &blobPath, &sizeBytes)
	if err != nil {
		t.Fatalf("query persisted message: %v", err)
	}
	if uid != 1 {
		t.Errorf("first uid = %d, want 1 (folders.uid_next allocation)", uid)
	}
	raw, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("EML file missing on disk: %v", err)
	}
	sum := sha256.Sum256(raw)
	var dbSum string
	if err := pool.QueryRow(ctx,
		`SELECT content_sha256 FROM message_blobs LIMIT 1`).Scan(&dbSum); err != nil {
		t.Fatalf("blob row: %v", err)
	}
	if hex.EncodeToString(sum[:]) != dbSum {
		t.Errorf("on-disk sha256 %x != db %s", sum, dbSum)
	}

	var outboxCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM domain_events_outbox WHERE event_type = 'EmailReceived'`).Scan(&outboxCount); err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if outboxCount != 1 {
		t.Errorf("outbox events = %d, want 1", outboxCount)
	}

	// --- 7. Dedup: identical message again => 1 blob, ref_count 2, uid 2 --
	deliver()
	var blobs, refs int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), max(ref_count) FROM message_blobs`).Scan(&blobs, &refs); err != nil {
		t.Fatalf("dedup query: %v", err)
	}
	if blobs != 1 || refs != 2 {
		t.Errorf("blobs=%d refs=%d, want 1 blob with ref_count 2 (PLAN section 9 dedup)", blobs, refs)
	}
	var maxUID int64
	if err := pool.QueryRow(ctx, `SELECT max(uid) FROM email_messages`).Scan(&maxUID); err != nil {
		t.Fatalf("uid query: %v", err)
	}
	if maxUID != 2 {
		t.Errorf("max uid = %d, want 2 (sequential per folder)", maxUID)
	}

	// --- 8. Folder/mailbox counters kept in sync with persisted messages --
	t.Run("counters_updated", func(t *testing.T) {
		var unread, total int
		var used int64
		err := pool.QueryRow(ctx, `
			SELECT f.unread_count, f.total_count, mb.used_bytes
			FROM folders f JOIN mailboxes mb ON mb.id = f.mailbox_id
			WHERE f.special_use = 'inbox' AND mb.email_address = 'postmaster@example.test'
		`).Scan(&unread, &total, &used)
		if err != nil {
			t.Fatalf("counters query: %v", err)
		}
		if unread != 2 || total != 2 || used != sizeBytes*2 {
			t.Errorf("unread=%d total=%d used=%d, want unread=2 total=2 used=%d (PLAN section 6.1 '++counters')", unread, total, used, sizeBytes*2)
		}
	})

	// --- 9. A full mailbox rejects RCPT with 452 4.2.2, in-session --------
	t.Run("quota_rejected_when_mailbox_full", func(t *testing.T) {
		domainID := "00000000-0000-0000-0000-000000000001"
		fullMailboxID := "10000000-0000-0000-0000-000000000099"
		fullAddress := "full@example.test"
		if _, err := pool.Exec(ctx, `
			INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, quota_bytes, used_bytes)
			VALUES ($1, $2, 'full', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG', 1000, 1000)
		`, fullMailboxID, domainID, fullAddress); err != nil {
			t.Fatalf("seed full mailbox: %v", err)
		}

		c, err := smtp.Dial(addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer c.Close()
		if err := c.Mail("sender@remote.example"); err != nil {
			t.Fatalf("MAIL FROM: %v", err)
		}
		err = c.Rcpt(fullAddress)
		if err == nil {
			t.Fatal("RCPT to a full mailbox succeeded; want 452 4.2.2")
		}
		if !strings.Contains(err.Error(), "452") {
			t.Errorf("full-mailbox RCPT error = %q, want SMTP 452", err)
		}
	})

	// --- 10. An alias fans out to its destination mailbox -----------------
	t.Run("alias_resolves_to_destination_mailbox", func(t *testing.T) {
		domainID := "00000000-0000-0000-0000-000000000001"
		aliasDestMailboxID := "10000000-0000-0000-0000-000000000098"
		aliasDestAddress := "alias-dest@example.test"
		aliasAddress := "hello@example.test"
		if _, err := pool.Exec(ctx, `
			INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash)
			VALUES ($1, $2, 'alias-dest', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG')
		`, aliasDestMailboxID, domainID, aliasDestAddress); err != nil {
			t.Fatalf("seed alias destination mailbox: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')
		`, "10000000-0000-0000-0000-000000000097", aliasDestMailboxID); err != nil {
			t.Fatalf("seed alias destination inbox folder: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO aliases (id, domain_id, source_address, destination_addresses)
			VALUES ($1, $2, $3, $4)
		`, "10000000-0000-0000-0000-000000000096", domainID, aliasAddress, []string{aliasDestAddress}); err != nil {
			t.Fatalf("seed alias: %v", err)
		}

		if err := smtp.SendMail(addr, nil, "sender@remote.example",
			[]string{aliasAddress}, []byte(testMessage)); err != nil {
			t.Fatalf("SendMail to alias: %v", err)
		}

		var count int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM email_messages WHERE mailbox_id = $1`, aliasDestMailboxID).Scan(&count); err != nil {
			t.Fatalf("query alias destination messages: %v", err)
		}
		if count != 1 {
			t.Errorf("messages persisted for alias destination mailbox = %d, want 1", count)
		}
	})

	_ = fmt.Sprintf // keep fmt imported if assertions above change
}

// repoRoot walks up from the test file location to the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The marker is a migrations directory that actually holds migrations.
	//
	// It used to be PLAN.md, which .gitignore excludes on purpose, so this
	// could never succeed in CI. Matching a bare "migrations" directory was
	// the next attempt and broke as soon as services/migrations appeared -
	// walking up from a test hits that one first and stops at services/.
	// Requiring the SQL to be there is what the callers actually need.
	for i := 0; i < 8; i++ {
		matches, _ := filepath.Glob(filepath.Join(dir, "migrations", "*.up.sql"))
		if len(matches) > 0 {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate repository root (no migrations/*.up.sql in any parent)")
	return ""
}
