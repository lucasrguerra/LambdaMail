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
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	imappresentation "lambdamail/protocols/internal/presentation/imap"
)

func TestImapIdleAndCondStoreEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54331). // distinct port for concurrent e2e test execution
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54331/postgres?sslmode=disable"
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

	// --- Seed mailbox --------------------------------------------------------
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

	spool := t.TempDir()
	mailboxRepo := postgres.NewMailboxRepository(pool)
	inboundUC := appusecase.NewProcessInboundEmailUseCase(
		mailboxRepo,
		diskstorage.NewLocalDiskBlobStorage(pool, spool),
		postgres.NewInboundMessageRepository(pool),
	)

	imapFolderRepo := postgres.NewImapFolderRepository(pool)
	imapUC := appusecase.NewImapSessionUseCase(
		postgres.NewAuthRepository(pool),
		imapFolderRepo,
		postgres.NewMessageQueryRepository(pool),
		postgres.NewFlagRepository(pool),
		diskstorage.NewLocalDiskBlobReader(pool),
		postgres.NewExpungeRepository(pool),
		postgres.NewCopyRepository(pool),
	)
	inboundUC.SetTrackerManager(imapUC.GetTrackerManager(), imapFolderRepo)

	server := imapserver.New(&imapserver.Options{
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go server.Serve(ln)
	defer server.Close()

	// Deliver 1 initial message
	recipientRecord, err := mailboxRepo.FindActiveByAddress(ctx, "user@example.test")
	if err != nil || recipientRecord == nil {
		t.Fatalf("find recipient record: %v", err)
	}
	testMessage := "From: sender@remote.example\r\nTo: user@example.test\r\nSubject: condstore test\r\n\r\nInitial email.\r\n"
	if err := inboundUC.Handle(ctx, appusecase.ProcessInboundEmailInput{
		Sender:             "sender@remote.example",
		Recipients:         []port.MailboxRecord{*recipientRecord},
		RecipientAddresses: []string{"user@example.test"},
		Body:               bytes.NewBufferString(testMessage),
	}); err != nil {
		t.Fatalf("seed message: %v", err)
	}

	// Connect Client A with UnilateralDataHandler to receive live IDLE updates
	mailboxUpdates := make(chan *imapclient.UnilateralDataMailbox, 10)
	clientOptions := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				mailboxUpdates <- data
			},
		},
	}
	clientA, err := imapclient.DialInsecure(ln.Addr().String(), clientOptions)
	if err != nil {
		t.Fatalf("dial clientA: %v", err)
	}
	defer clientA.Close()

	if err := clientA.Login("user@example.test", password).Wait(); err != nil {
		t.Fatalf("LOGIN clientA: %v", err)
	}

	selectData, err := clientA.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatalf("SELECT INBOX: %v", err)
	}

	if selectData.NumMessages != 1 {
		t.Fatalf("expected NumMessages=1, got %d", selectData.NumMessages)
	}

	// FETCH message 1
	fetchItems, err := clientA.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{Flags: true, UID: true}).Collect()
	if err != nil {
		t.Fatalf("FETCH message 1: %v", err)
	}
	if len(fetchItems) != 1 {
		t.Fatalf("expected 1 fetch item, got %d", len(fetchItems))
	}

	// STORE flag on message 1
	if err := clientA.Store(imap.SeqSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagFlagged}}, nil).Close(); err != nil {
		t.Fatalf("STORE flag failed: %v", err)
	}

	// SEARCH test by flag
	searchRes, err := clientA.Search(&imap.SearchCriteria{Flag: []imap.Flag{imap.FlagFlagged}}, nil).Wait()
	if err != nil {
		t.Fatalf("SEARCH FLAGGED: %v", err)
	}
	if len(searchRes.AllSeqNums()) != 1 {
		t.Fatalf("SEARCH FLAGGED expected 1 result, got %d (all=%v)", len(searchRes.AllSeqNums()), searchRes.AllSeqNums())
	}

	// --- IDLE test -----------------------------------------------------------
	idleCmd, err := clientA.Idle()
	if err != nil {
		t.Fatalf("start IDLE: %v", err)
	}

	// Concurrently deliver a 2nd message while clientA is IDLE
	newMessage := "From: sender2@remote.example\r\nTo: user@example.test\r\nSubject: idle update\r\n\r\nRealtime email.\r\n"
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = inboundUC.Handle(context.Background(), appusecase.ProcessInboundEmailInput{
			Sender:             "sender2@remote.example",
			Recipients:         []port.MailboxRecord{*recipientRecord},
			RecipientAddresses: []string{"user@example.test"},
			Body:               bytes.NewBufferString(newMessage),
		})
	}()

	// Wait for clientA to receive mailbox update notification via UnilateralDataHandler
	select {
	case update := <-mailboxUpdates:
		if update == nil || update.NumMessages == nil || *update.NumMessages != 2 {
			t.Fatalf("expected update.NumMessages=2, got %+v", update)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for IDLE notification")
	}

	if err := idleCmd.Close(); err != nil {
		t.Fatalf("close IDLE: %v", err)
	}
}
