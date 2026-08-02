package e2e

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
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
)

func TestImapCoreEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54330). // distinct from the SMTP E2E test's 54329, so both can run concurrently
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54330/postgres?sslmode=disable"
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

	// --- Seed a real mailbox with a known password -------------------------
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
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'postmaster', 'postmaster@example.test', $3)`, mailboxID, domainID, hash); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`, folderID, mailboxID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	// --- Seed a real message via F1's inbound use case (not a raw INSERT - --
	// --- this proves the IMAP side reads what the SMTP side really wrote)  --
	spool := t.TempDir()
	inboundUC := appusecase.NewProcessInboundEmailUseCase(
		postgres.NewMailboxRepository(pool),
		diskstorage.NewLocalDiskBlobStorage(pool, spool),
		postgres.NewInboundMessageRepository(pool),
	)

	mailboxRepo := postgres.NewMailboxRepository(pool)
	recipientRecord, err := mailboxRepo.FindActiveByAddress(ctx, "postmaster@example.test")
	if err != nil {
		t.Fatalf("resolve recipient for seeding: %v", err)
	}
	if recipientRecord == nil {
		t.Fatal("recipient not found - seeding order bug: mailbox must exist before this call")
	}

	testMessage := "From: sender@remote.example\r\nTo: postmaster@example.test\r\nSubject: imap e2e\r\n\r\nHello via IMAP.\r\n"
	if err := inboundUC.Handle(ctx, appusecase.ProcessInboundEmailInput{
		Sender:             "sender@remote.example",
		Recipients:         []port.MailboxRecord{*recipientRecord},
		RecipientAddresses: []string{"postmaster@example.test"},
		Body:               bytes.NewBufferString(testMessage),
	}); err != nil {
		t.Fatalf("seed message via SMTP use case: %v", err)
	}

	// --- Real IMAP server, wired exactly like main.go ----------------------
	imapUC := appusecase.NewImapSessionUseCase(
		postgres.NewAuthRepository(pool),
		postgres.NewImapFolderRepository(pool),
		postgres.NewMessageQueryRepository(pool),
		postgres.NewFlagRepository(pool),
		diskstorage.NewLocalDiskBlobReader(pool),
		postgres.NewExpungeRepository(pool),
		postgres.NewCopyRepository(pool),
	)
	server := imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return imappresentation.NewSession(c, imapUC)
		},
		// Mirror main.go's Caps: without this, imapserver defaults to bare
		// IMAP4rev1 and won't advertise MOVE/UIDPLUS, which the SEARCH/COPY/
		// EXPUNGE scenarios below (and the server-side COPYUID response)
		// depend on the client library correctly attributing.
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapMove:      {},
			imap.CapUIDPlus:   {},
		},
		InsecureAuth: true, // no TLS in this test - InsecureAuth lets LOGIN proceed over plaintext
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go server.Serve(ln)
	defer server.Close()

	// --- Real IMAP client drives the session --------------------------------
	client, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := client.Login("postmaster@example.test", password).Wait(); err != nil {
		t.Fatalf("LOGIN: %v", err)
	}

	selectData, err := client.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if selectData.NumMessages != 1 {
		t.Errorf("NumMessages = %d, want 1", selectData.NumMessages)
	}

	// A real IMAP client's first action after LOGIN is typically
	// LIST "" "*" - drive Session.List through the real imapserver stack
	// (unlike the unit tests, which can't construct a *imapserver.ListWriter
	// directly) and confirm INBOX comes back.
	listEntries, err := client.List("", "*", nil).Collect()
	if err != nil {
		t.Fatalf("LIST: %v", err)
	}
	foundInbox := false
	for _, entry := range listEntries {
		if entry.Mailbox == "INBOX" {
			foundInbox = true
		}
	}
	if !foundInbox {
		t.Errorf("LIST results = %+v, want an entry for INBOX", listEntries)
	}

	fetchCmd := client.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{
		UID: true, Flags: true, Envelope: true,
		BodySection: []*imap.FetchItemBodySection{{}},
	})
	messages, err := fetchCmd.Collect()
	if err != nil {
		t.Fatalf("FETCH: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("fetched %d messages, want 1", len(messages))
	}
	if messages[0].Envelope == nil || messages[0].Envelope.Subject != "imap e2e" {
		t.Errorf("Envelope.Subject = %+v, want \"imap e2e\"", messages[0].Envelope)
	}

	// Client.Store returns *FetchCommand (like Fetch does), not a plain
	// Command - it has Collect(), not Wait(), since STORE (without SILENT)
	// streams back the updated FETCH data per RFC 3501.
	if _, err := client.Store(imap.UIDSetNum(1), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen},
	}, nil).Collect(); err != nil {
		t.Fatalf("STORE: %v", err)
	}

	fetchAfterStore, err := client.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil {
		t.Fatalf("FETCH after STORE: %v", err)
	}
	found := false
	for _, f := range fetchAfterStore[0].Flags {
		if f == imap.FlagSeen {
			found = true
		}
	}
	if !found {
		t.Errorf("flags after STORE = %v, want to include \\Seen", fetchAfterStore[0].Flags)
	}

	// --- SEARCH: the STORE above set \Seen on the one seeded message -------
	//
	// SearchData.Count is only populated when the client requests an
	// ESEARCH RETURN option (COUNT/MIN/MAX/ALL) - without SearchOptions the
	// client sends a bare SEARCH command and the server replies with the
	// classic "* SEARCH <seq-nums>" response, which the client library only
	// folds into SearchData.All (verified against imapclient/search.go's
	// handleSearch, which only appends to cmd.data.All for that response
	// form, vs handleESearch which is the only path that sets data.Count).
	// So matches must be counted via AllSeqNums(), not Count.
	searchData, err := client.Search(&imap.SearchCriteria{
		Flag: []imap.Flag{imap.FlagSeen},
	}, nil).Wait()
	if err != nil {
		t.Fatalf("SEARCH: %v", err)
	}
	if matched := searchData.AllSeqNums(); len(matched) != 1 {
		t.Errorf("SEARCH \\Seen matched %v, want exactly 1 sequence number", matched)
	}

	// --- COPY + EXPUNGE: seed a second folder the same way INBOX was seeded,
	// copy the message into it, verify blob dedup accounting in Postgres,
	// then expunge the (now \Deleted) INBOX original and confirm EXPUNGE
	// only touched the selected folder.
	archiveFolderID := "00000000-0000-0000-0000-000000000004"
	if _, err := pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'Archive', 'archive')`, archiveFolderID, mailboxID); err != nil {
		t.Fatalf("seed Archive folder: %v", err)
	}

	copyData, err := client.Copy(imap.UIDSetNum(1), "Archive").Wait()
	if err != nil {
		t.Fatalf("COPY: %v", err)
	}
	destUIDs, ok := copyData.DestUIDs.Nums()
	if !ok || len(destUIDs) != 1 {
		t.Fatalf("COPY DestUIDs = %v (ok=%v), want exactly 1 UID", destUIDs, ok)
	}

	var inboxBlobID, archiveBlobID string
	if err := pool.QueryRow(ctx, `SELECT blob_id FROM email_messages WHERE folder_id = $1 AND uid = 1 AND expunged_at IS NULL`, folderID).Scan(&inboxBlobID); err != nil {
		t.Fatalf("query INBOX blob_id: %v", err)
	}
	var archiveCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM email_messages WHERE folder_id = $1 AND expunged_at IS NULL`, archiveFolderID).Scan(&archiveCount); err != nil {
		t.Fatalf("count Archive messages: %v", err)
	}
	if archiveCount != 1 {
		t.Errorf("Archive message count = %d, want 1", archiveCount)
	}
	if err := pool.QueryRow(ctx, `SELECT blob_id FROM email_messages WHERE folder_id = $1 AND expunged_at IS NULL`, archiveFolderID).Scan(&archiveBlobID); err != nil {
		t.Fatalf("query Archive blob_id: %v", err)
	}
	if archiveBlobID != inboxBlobID {
		t.Errorf("Archive blob_id = %s, want same blob as INBOX (%s)", archiveBlobID, inboxBlobID)
	}
	var refCount int
	if err := pool.QueryRow(ctx, `SELECT ref_count FROM message_blobs WHERE id = $1`, inboxBlobID).Scan(&refCount); err != nil {
		t.Fatalf("query blob ref_count: %v", err)
	}
	if refCount != 2 {
		t.Errorf("message_blobs.ref_count = %d, want 2 after COPY", refCount)
	}

	if _, err := client.Store(imap.UIDSetNum(1), &imap.StoreFlags{
		Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted},
	}, nil).Collect(); err != nil {
		t.Fatalf("STORE \\Deleted: %v", err)
	}

	if _, err := client.Expunge().Collect(); err != nil {
		t.Fatalf("EXPUNGE: %v", err)
	}

	var inboxCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM email_messages WHERE folder_id = $1 AND expunged_at IS NULL`, folderID).Scan(&inboxCount); err != nil {
		t.Fatalf("count INBOX messages after EXPUNGE: %v", err)
	}
	if inboxCount != 0 {
		t.Errorf("INBOX message count after EXPUNGE = %d, want 0", inboxCount)
	}

	var archiveCountAfter int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM email_messages WHERE folder_id = $1 AND expunged_at IS NULL`, archiveFolderID).Scan(&archiveCountAfter); err != nil {
		t.Fatalf("count Archive messages after EXPUNGE: %v", err)
	}
	if archiveCountAfter != 1 {
		t.Errorf("Archive message count after INBOX EXPUNGE = %d, want 1 (untouched)", archiveCountAfter)
	}
}
