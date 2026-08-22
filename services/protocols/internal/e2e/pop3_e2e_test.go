package e2e

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"lambdamail/protocols/internal/application/port"
	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	pop3presentation "lambdamail/protocols/internal/presentation/pop3"
)

func TestPop3EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(15432). // distinct port for POP3 e2e test execution
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:15432/postgres?sslmode=disable"
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	root := repoRoot(t)
	sql, err := os.ReadFile(filepath.Join(root, "migrations", "0001_init_schema.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	// --- Seed domain, mailbox, INBOX -----------------------------------------
	password := "correct horse battery staple"
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	domainID := "00000000-0000-0000-0000-000000000001"
	mailboxID := "00000000-0000-0000-0000-000000000002"
	folderID := "00000000-0000-0000-0000-000000000003"
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'example.test', 'example.test')`, domainID); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', 'user@example.test', $3)`, mailboxID, domainID, hash); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`, folderID, mailboxID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	// Deliver 1 email via SMTP use case
	spool := t.TempDir()
	mailboxRepo := postgres.NewMailboxRepository(pool)
	inboundUC := appusecase.NewProcessInboundEmailUseCase(
		mailboxRepo,
		diskstorage.NewLocalDiskBlobStorage(pool, spool),
		postgres.NewInboundMessageRepository(pool),
	)

	recipientRecord, err := mailboxRepo.FindActiveByAddress(ctx, "user@example.test")
	if err != nil || recipientRecord == nil {
		t.Fatalf("find recipient record: %v", err)
	}
	testMessage := "From: sender@remote.example\r\nTo: user@example.test\r\nSubject: pop3 test\r\n\r\n.Dot line test\r\nPOP3 body content.\r\n"
	if err := inboundUC.Handle(ctx, appusecase.ProcessInboundEmailInput{
		Sender:             "sender@remote.example",
		Recipients:         []port.MailboxRecord{*recipientRecord},
		RecipientAddresses: []string{"user@example.test"},
		Body:               bytes.NewBufferString(testMessage),
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	// Setup POP3 Server
	pop3UC := appusecase.NewPop3SessionUseCase(
		postgres.NewAuthRepository(pool),
		postgres.NewImapFolderRepository(pool),
		postgres.NewMessageQueryRepository(pool),
		diskstorage.NewLocalDiskBlobReader(pool),
		postgres.NewExpungeRepository(pool),
	)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	srv := pop3presentation.NewServer(ln.Addr().String(), pop3UC, nil)
	go srv.Serve(ln)
	defer srv.Close()

	// Dial POP3 server
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial POP3: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	readLine := func() string {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read line: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}

	sendCmd := func(cmd string) {
		writer.WriteString(cmd + "\r\n")
		writer.Flush()
	}

	// 1. Greeting
	greeting := readLine()
	if !strings.HasPrefix(greeting, "+OK") {
		t.Fatalf("expected greeting +OK, got %q", greeting)
	}

	// 2. CAPA
	sendCmd("CAPA")
	if capaHead := readLine(); !strings.HasPrefix(capaHead, "+OK") {
		t.Fatalf("CAPA head = %q", capaHead)
	}
	for {
		if l := readLine(); l == "." {
			break
		}
	}

	// 3. USER / PASS
	sendCmd("USER user@example.test")
	if userResp := readLine(); !strings.HasPrefix(userResp, "+OK") {
		t.Fatalf("USER resp = %q", userResp)
	}

	sendCmd("PASS correct horse battery staple")
	if passResp := readLine(); !strings.HasPrefix(passResp, "+OK") {
		t.Fatalf("PASS resp = %q", passResp)
	}

	// 4. STAT
	sendCmd("STAT")
	statResp := readLine()
	if !strings.HasPrefix(statResp, "+OK 1 ") {
		t.Fatalf("STAT expected 1 message, got %q", statResp)
	}

	// 5. LIST
	sendCmd("LIST 1")
	listResp := readLine()
	if !strings.HasPrefix(listResp, "+OK 1 ") {
		t.Fatalf("LIST 1 expected +OK 1, got %q", listResp)
	}

	// 6. UIDL
	sendCmd("UIDL 1")
	uidlResp := readLine()
	if !strings.HasPrefix(uidlResp, "+OK 1 ") {
		t.Fatalf("UIDL 1 expected +OK 1, got %q", uidlResp)
	}

	// 7. TOP 1 1
	sendCmd("TOP 1 1")
	if topHead := readLine(); !strings.HasPrefix(topHead, "+OK") {
		t.Fatalf("TOP 1 1 head = %q", topHead)
	}
	for {
		if l := readLine(); l == "." {
			break
		}
	}

	// 8. RETR 1
	sendCmd("RETR 1")
	if retrHead := readLine(); !strings.HasPrefix(retrHead, "+OK") {
		t.Fatalf("RETR 1 head = %q", retrHead)
	}
	var retrBody []string
	for {
		l := readLine()
		if l == "." {
			break
		}
		retrBody = append(retrBody, l)
	}
	if len(retrBody) == 0 {
		t.Fatal("expected non-empty RETR body")
	}

	// 9. DELE 1
	sendCmd("DELE 1")
	if deleResp := readLine(); !strings.HasPrefix(deleResp, "+OK") {
		t.Fatalf("DELE 1 resp = %q", deleResp)
	}

	// 10. QUIT (commits expunge to database)
	sendCmd("QUIT")
	if quitResp := readLine(); !strings.HasPrefix(quitResp, "+OK") {
		t.Fatalf("QUIT resp = %q", quitResp)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify in Postgres that the message was marked expunged
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_messages WHERE folder_id = $1 AND expunged_at IS NOT NULL`, folderID).Scan(&count); err != nil {
		t.Fatalf("query expunged_at count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 expunged message in DB, got %d", count)
	}
}
