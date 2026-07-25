package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"lambdamail/protocols/internal/application/port"
	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	imappresentation "lambdamail/protocols/internal/presentation/imap"
	managesievepresentation "lambdamail/protocols/internal/presentation/managesieve"
	pop3presentation "lambdamail/protocols/internal/presentation/pop3"
)

func TestF2CompleteSuiteEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("complete F2 e2e test skipped in -short mode")
	}
	ctx := context.Background()

	// 1. Embedded Postgres Setup
	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54335).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54335/postgres?sslmode=disable"
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

	// 2. Seed domain, mailbox, INBOX, Archive
	password := "correct horse battery staple"
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	domainID := "00000000-0000-0000-0000-000000000001"
	mailboxID := "00000000-0000-0000-0000-000000000002"
	inboxID := "00000000-0000-0000-0000-000000000003"
	archiveID := "00000000-0000-0000-0000-000000000004"

	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'example.test', 'example.test')`, domainID); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', 'user@example.test', $3)`, mailboxID, domainID, hash); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`, inboxID, mailboxID); err != nil {
		t.Fatalf("seed INBOX: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'Archive', 'archive')`, archiveID, mailboxID); err != nil {
		t.Fatalf("seed Archive: %v", err)
	}

	// 3. Initialize Use Cases & Protocol Servers
	mailboxRepo := postgres.NewMailboxRepository(pool)
	authRepo := postgres.NewAuthRepository(pool)
	imapFolderRepo := postgres.NewImapFolderRepository(pool)
	messageQueryRepo := postgres.NewMessageQueryRepository(pool)
	flagRepo := postgres.NewFlagRepository(pool)
	expungeRepo := postgres.NewExpungeRepository(pool)
	copyRepo := postgres.NewCopyRepository(pool)
	sieveRepo := postgres.NewSieveRepository(pool)
	spool := t.TempDir()
	blobStorage := diskstorage.NewLocalDiskBlobStorage(pool, spool)
	blobReader := diskstorage.NewLocalDiskBlobReader(pool)

	inboundUC := appusecase.NewProcessInboundEmailUseCase(mailboxRepo, blobStorage, postgres.NewInboundMessageRepository(pool))
	imapUC := appusecase.NewImapSessionUseCase(authRepo, imapFolderRepo, messageQueryRepo, flagRepo, blobReader, expungeRepo, copyRepo)
	inboundUC.SetTrackerManager(imapUC.GetTrackerManager(), imapFolderRepo)

	pop3UC := appusecase.NewPop3SessionUseCase(authRepo, imapFolderRepo, messageQueryRepo, blobReader, expungeRepo)
	sieveUC := appusecase.NewManageSieveSessionUseCase(authRepo, sieveRepo)

	// --- Deliver 2 initial emails via SMTP inbound ---------------------------
	recipientRecord, err := mailboxRepo.FindActiveByAddress(ctx, "user@example.test")
	if err != nil || recipientRecord == nil {
		t.Fatalf("find recipient: %v", err)
	}
	msg1Raw := "From: sender1@remote.test\r\nTo: user@example.test\r\nSubject: Important message\r\n\r\n.Dot line test\r\nHello World\r\n"
	if err := inboundUC.Handle(ctx, appusecase.ProcessInboundEmailInput{
		Sender:             "sender1@remote.test",
		Recipients:         []port.MailboxRecord{*recipientRecord},
		RecipientAddresses: []string{"user@example.test"},
		Body:               bytes.NewBufferString(msg1Raw),
	}); err != nil {
		t.Fatalf("deliver msg1: %v", err)
	}

	msg2Raw := "From: sender2@remote.test\r\nTo: user@example.test\r\nSubject: Newsletter\r\n\r\nUnsubscribe here.\r\n"
	if err := inboundUC.Handle(ctx, appusecase.ProcessInboundEmailInput{
		Sender:             "sender2@remote.test",
		Recipients:         []port.MailboxRecord{*recipientRecord},
		RecipientAddresses: []string{"user@example.test"},
		Body:               bytes.NewBufferString(msg2Raw),
	}); err != nil {
		t.Fatalf("deliver msg2: %v", err)
	}

	// Launch IMAP Server
	imapServer := imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return imappresentation.NewSession(c, imapUC)
		},
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapMove:      {},
			imap.CapUIDPlus:   {},
			imap.CapIdle:      {},
			imap.CapCondStore: {},
		},
		InsecureAuth: true,
	})
	imapLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("imap listen: %v", err)
	}
	go imapServer.Serve(imapLn)
	defer imapServer.Close()

	// Launch POP3 Server
	pop3Ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pop3 listen: %v", err)
	}
	pop3Server := pop3presentation.NewServer(pop3Ln.Addr().String(), pop3UC, nil)
	go pop3Server.Serve(pop3Ln)
	defer pop3Server.Close()

	// Launch ManageSieve Server
	sieveLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("sieve listen: %v", err)
	}
	sieveServer := managesievepresentation.NewServer(sieveLn.Addr().String(), sieveUC, nil)
	go sieveServer.Serve(sieveLn)
	defer sieveServer.Close()

	// =========================================================================
	// SECTION A: IMAP E2E (SELECT, FETCH, STORE, SEARCH, IDLE, CONDSTORE, MOVE)
	// =========================================================================
	mailboxUpdates := make(chan *imapclient.UnilateralDataMailbox, 10)
	clientA, err := imapclient.DialInsecure(imapLn.Addr().String(), &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				mailboxUpdates <- data
			},
		},
	})
	if err != nil {
		t.Fatalf("dial clientA: %v", err)
	}
	defer clientA.Close()

	if err := clientA.Login("user@example.test", password).Wait(); err != nil {
		t.Fatalf("clientA login: %v", err)
	}

	selectData, err := clientA.Select("INBOX", nil).Wait()
	if err != nil || selectData.NumMessages != 2 {
		t.Fatalf("clientA select INBOX: err=%v, numMsg=%d", err, selectData.NumMessages)
	}

	// STORE +FLAGS \Flagged on Message 1
	if err := clientA.Store(imap.SeqSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagFlagged}}, nil).Close(); err != nil {
		t.Fatalf("clientA store flagged: %v", err)
	}

	// SEARCH FLAGGED
	searchRes, err := clientA.Search(&imap.SearchCriteria{Flag: []imap.Flag{imap.FlagFlagged}}, nil).Wait()
	if err != nil || len(searchRes.AllSeqNums()) != 1 {
		t.Fatalf("clientA search flagged: err=%v, count=%d", err, len(searchRes.AllSeqNums()))
	}

	// SEARCH by Subject header
	headerRes, err := clientA.Search(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "Important"}}}, nil).Wait()
	if err != nil || len(headerRes.AllSeqNums()) != 1 {
		t.Fatalf("clientA search header: err=%v, count=%d", err, len(headerRes.AllSeqNums()))
	}

	// SEARCH by Body text
	textRes, err := clientA.Search(&imap.SearchCriteria{Text: []string{"World"}}, nil).Wait()
	if err != nil || len(textRes.AllSeqNums()) != 1 {
		t.Fatalf("clientA search text: err=%v, count=%d", err, len(textRes.AllSeqNums()))
	}

	// Start IDLE on Client A
	idleCmd, err := clientA.Idle()
	if err != nil {
		t.Fatalf("clientA idle start: %v", err)
	}

	// Concurrently deliver a 3rd message while Client A is in IDLE
	msg3Raw := "From: sender3@remote.test\r\nTo: user@example.test\r\nSubject: Realtime message\r\n\r\nIDLE push notification test\r\n"
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = inboundUC.Handle(context.Background(), appusecase.ProcessInboundEmailInput{
			Sender:             "sender3@remote.test",
			Recipients:         []port.MailboxRecord{*recipientRecord},
			RecipientAddresses: []string{"user@example.test"},
			Body:               bytes.NewBufferString(msg3Raw),
		})
	}()

	// Verify Client A receives IDLE notification
	select {
	case update := <-mailboxUpdates:
		if update == nil || update.NumMessages == nil || *update.NumMessages != 3 {
			t.Fatalf("expected IDLE update.NumMessages=3, got %+v", update)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for IDLE update on Client A")
	}

	if err := idleCmd.Close(); err != nil {
		t.Fatalf("clientA idle stop: %v", err)
	}

	// MOVE Message 3 to Archive folder (RFC 6851)
	if _, err := clientA.Move(imap.SeqSetNum(3), "Archive").Wait(); err != nil {
		t.Fatalf("clientA move: %v", err)
	}

	archiveSelect, err := clientA.Select("Archive", nil).Wait()
	if err != nil || archiveSelect.NumMessages != 1 {
		t.Fatalf("select Archive: err=%v, numMsg=%d", err, archiveSelect.NumMessages)
	}

	// Re-select INBOX (now 2 messages remaining: msg1 and msg2)
	inboxSelect, err := clientA.Select("INBOX", nil).Wait()
	if err != nil || inboxSelect.NumMessages != 2 {
		t.Fatalf("re-select INBOX: err=%v, numMsg=%d", err, inboxSelect.NumMessages)
	}
	clientA.Close()

	// =========================================================================
	// SECTION B: POP3 E2E (STAT, LIST, UIDL, TOP, RETR, DELE, QUIT)
	// =========================================================================
	pop3Conn, err := net.Dial("tcp", pop3Ln.Addr().String())
	if err != nil {
		t.Fatalf("dial POP3: %v", err)
	}
	defer pop3Conn.Close()

	pop3Reader := bufio.NewReader(pop3Conn)
	pop3Writer := bufio.NewWriter(pop3Conn)

	readLinePop3 := func() string {
		line, err := pop3Reader.ReadString('\n')
		if err != nil {
			t.Fatalf("pop3 read: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}
	sendCmdPop3 := func(cmd string) {
		pop3Writer.WriteString(cmd + "\r\n")
		pop3Writer.Flush()
	}

	if g := readLinePop3(); !strings.HasPrefix(g, "+OK") {
		t.Fatalf("POP3 greeting = %q", g)
	}

	sendCmdPop3("CAPA")
	readLinePop3() // +OK
	for {
		if l := readLinePop3(); l == "." {
			break
		}
	}

	sendCmdPop3("USER user@example.test")
	readLinePop3()
	sendCmdPop3("PASS correct horse battery staple")
	if passResp := readLinePop3(); !strings.HasPrefix(passResp, "+OK") {
		t.Fatalf("POP3 PASS resp = %q", passResp)
	}

	sendCmdPop3("STAT")
	if statResp := readLinePop3(); !strings.HasPrefix(statResp, "+OK 2 ") {
		t.Fatalf("POP3 STAT = %q, expected 2 messages", statResp)
	}

	sendCmdPop3("LIST 1")
	if listResp := readLinePop3(); !strings.HasPrefix(listResp, "+OK 1 ") {
		t.Fatalf("POP3 LIST = %q", listResp)
	}

	sendCmdPop3("UIDL 1")
	if uidlResp := readLinePop3(); !strings.HasPrefix(uidlResp, "+OK 1 ") {
		t.Fatalf("POP3 UIDL = %q", uidlResp)
	}

	// TOP 1 1 verifying dot-stuffing ..Dot line test
	sendCmdPop3("TOP 1 1")
	if topHead := readLinePop3(); !strings.HasPrefix(topHead, "+OK") {
		t.Fatalf("POP3 TOP head = %q", topHead)
	}
	var topLines []string
	for {
		l := readLinePop3()
		if l == "." {
			break
		}
		topLines = append(topLines, l)
	}
	if len(topLines) < 5 || topLines[4] != "..Dot line test" {
		t.Fatalf("POP3 dot-stuffed TOP lines = %v", topLines)
	}

	sendCmdPop3("RETR 1")
	if retrHead := readLinePop3(); !strings.HasPrefix(retrHead, "+OK") {
		t.Fatalf("POP3 RETR head = %q", retrHead)
	}
	for {
		if l := readLinePop3(); l == "." {
			break
		}
	}

	sendCmdPop3("DELE 1")
	if deleResp := readLinePop3(); !strings.HasPrefix(deleResp, "+OK") {
		t.Fatalf("POP3 DELE = %q", deleResp)
	}

	sendCmdPop3("QUIT")
	if quitResp := readLinePop3(); !strings.HasPrefix(quitResp, "+OK") {
		t.Fatalf("POP3 QUIT = %q", quitResp)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify INBOX messages marked expunged in Postgres (msg3 moved + msg1 DELE'd)
	var expungedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_messages WHERE folder_id = $1 AND expunged_at IS NOT NULL`, inboxID).Scan(&expungedCount); err != nil || expungedCount != 2 {
		t.Fatalf("expected 2 expunged messages in INBOX, got count=%d, err=%v", expungedCount, err)
	}

	// =========================================================================
	// SECTION C: MANAGESIEVE E2E (CAPABILITY, AUTH, CHECKSCRIPT, PUT, LIST, DELETE)
	// =========================================================================
	sieveConn, err := net.Dial("tcp", sieveLn.Addr().String())
	if err != nil {
		t.Fatalf("dial ManageSieve: %v", err)
	}
	defer sieveConn.Close()

	sieveReader := bufio.NewReader(sieveConn)
	sieveWriter := bufio.NewWriter(sieveConn)

	readLineSieve := func() string {
		line, err := sieveReader.ReadString('\n')
		if err != nil {
			t.Fatalf("sieve read: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}
	sendCmdSieve := func(cmd string) {
		sieveWriter.WriteString(cmd + "\r\n")
		sieveWriter.Flush()
	}

	for {
		if l := readLineSieve(); strings.HasPrefix(l, "OK") {
			break
		}
	}

	sendCmdSieve("CAPABILITY")
	for {
		if l := readLineSieve(); strings.HasPrefix(l, "OK") {
			break
		}
	}

	saslPayload := base64.StdEncoding.EncodeToString([]byte("\x00user@example.test\x00correct horse battery staple"))
	sendCmdSieve(fmt.Sprintf("AUTHENTICATE \"PLAIN\" %s", saslPayload))
	if authResp := readLineSieve(); !strings.HasPrefix(authResp, "OK") {
		t.Fatalf("ManageSieve AUTH = %q", authResp)
	}

	sieveCode := `require ["fileinto"]; if header :is "Subject" "Spam" { fileinto "Junk"; }`
	sendCmdSieve(fmt.Sprintf("CHECKSCRIPT {%d+}\r\n%s", len(sieveCode), sieveCode))
	if checkResp := readLineSieve(); !strings.HasPrefix(checkResp, "OK") {
		t.Fatalf("CHECKSCRIPT = %q", checkResp)
	}

	sendCmdSieve(fmt.Sprintf("PUTSCRIPT \"auto_rules\" {%d+}\r\n%s", len(sieveCode), sieveCode))
	if putResp := readLineSieve(); !strings.HasPrefix(putResp, "OK") {
		t.Fatalf("PUTSCRIPT = %q", putResp)
	}

	sendCmdSieve("GETSCRIPT \"auto_rules\"")
	readLineSieve() // literal head
	gotCode := readLineSieve()
	if gotCode != sieveCode {
		t.Fatalf("GETSCRIPT code = %q, want %q", gotCode, sieveCode)
	}
	readLineSieve() // OK

	sendCmdSieve("SETACTIVE \"auto_rules\"")
	if activeResp := readLineSieve(); !strings.HasPrefix(activeResp, "OK") {
		t.Fatalf("SETACTIVE = %q", activeResp)
	}

	// Verify RFC 5804 §2.7: DELETESCRIPT on active script MUST fail
	sendCmdSieve("DELETESCRIPT \"auto_rules\"")
	if delActiveResp := readLineSieve(); !strings.HasPrefix(delActiveResp, "NO") {
		t.Fatalf("expected NO for deleting active script, got %q", delActiveResp)
	}

	sendCmdSieve("SETACTIVE \"\"")
	readLineSieve() // OK

	sendCmdSieve("DELETESCRIPT \"auto_rules\"")
	if delOk := readLineSieve(); !strings.HasPrefix(delOk, "OK") {
		t.Fatalf("DELETESCRIPT = %q", delOk)
	}

	sendCmdSieve("LOGOUT")
	if logoutResp := readLineSieve(); !strings.HasPrefix(logoutResp, "OK") {
		t.Fatalf("LOGOUT = %q", logoutResp)
	}

	// Final DB verification: sieve_scripts table should be empty
	var sieveCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sieve_scripts WHERE mailbox_id = $1`, mailboxID).Scan(&sieveCount); err != nil || sieveCount != 0 {
		t.Fatalf("expected 0 sieve scripts in DB, got count=%d, err=%v", sieveCount, err)
	}
}
