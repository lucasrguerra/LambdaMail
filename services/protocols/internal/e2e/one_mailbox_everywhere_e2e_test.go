package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	gosmtp "github.com/emersion/go-smtp"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/dkim"
	"lambdamail/protocols/internal/infrastructure/postgres"
	httppresentation "lambdamail/protocols/internal/presentation/http"
	imappresentation "lambdamail/protocols/internal/presentation/imap"
	smtppresentation "lambdamail/protocols/internal/presentation/smtp"
)

// TestOneMailboxEverywhere is the contract of the whole server: SMTP, IMAP and
// the webmail are three ways into the same mailbox, so whatever one does, the
// other two must see, with the same numbers.
//
// The webmail used to carry its own copy of the rules and its own queries, and
// the two stacks drifted apart: mail sent over SMTP never reached Sent, reading
// over IMAP never moved the unread badge, deleting in the webmail never gave the
// quota back. This test drives all three on one mailbox, mixing them, and checks
// after every step that each view agrees with the others and with the data.
func TestOneMailboxEverywhere(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(15451).
		RuntimePath(t.TempDir()).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	pool, err := postgres.NewPool(ctx, "postgres://postgres:postgres@localhost:15451/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()
	applyMigrations(t, ctx, pool)

	// ------------------------------------------------------------- fixtures
	const (
		address  = "plataforma@everywhere.test"
		password = "a long enough password"
	)
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	domainID, mailboxID := uuid.New(), uuid.New()
	pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'everywhere.test', 'everywhere.test')`, domainID)
	pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'plataforma', $3, $4)`,
		mailboxID, domainID, address, hash)
	// The standard folders come from the one definition in the database
	if _, err := pool.Exec(ctx, `SELECT provision_default_folders($1)`, mailboxID); err != nil {
		t.Fatalf("provision folders: %v", err)
	}

	blobs := diskstorage.NewLocalDiskBlobStorage(pool, t.TempDir())
	blobReader := diskstorage.NewLocalDiskBlobReader(pool)
	authRepo := postgres.NewAuthRepository(pool)
	messages := postgres.NewInboundMessageRepository(pool)

	// Three messages arrive from outside, as the SMTP side would file them
	inbound := appusecase.NewProcessInboundEmailUseCase(postgres.NewMailboxRepository(pool), blobs, messages)
	target, err := postgres.NewMailboxRepository(pool).FindActiveByAddress(ctx, address)
	if err != nil || target == nil {
		t.Fatalf("resolve mailbox: %v", err)
	}
	for i := 1; i <= 3; i++ {
		raw := fmt.Sprintf("From: alice@remote.test\r\nTo: %s\r\nSubject: inbound %d\r\nMessage-ID: <in%d@remote.test>\r\n\r\nbody %d\r\n", address, i, i, i)
		if err := inbound.Handle(ctx, appusecase.ProcessInboundEmailInput{
			Sender: "alice@remote.test", Recipients: []port.MailboxRecord{*target}, RecipientAddresses: []string{address}, Body: strings.NewReader(raw),
		}); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}

	// -------------------------------------------- the three ways in, wired as main.go
	submissionUC := appusecase.NewProcessOutboundEmailUseCase(authRepo, postgres.NewOutboundRepository(pool), blobs, dkim.NewSigner(nil))
	submissionUC.SetSentCopies(messages)

	smtpServer := gosmtp.NewServer(smtppresentation.NewSubmissionBackend(submissionUC))
	smtpServer.Domain = "mail.everywhere.test"
	smtpServer.TLSConfig = &tls.Config{Certificates: []tls.Certificate{selfSignedTLSCert(t)}}
	smtpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer smtpLn.Close()
	go smtpServer.Serve(smtpLn)

	imapUC := appusecase.NewImapSessionUseCase(
		authRepo, postgres.NewImapFolderRepository(pool), postgres.NewMessageQueryRepository(pool),
		postgres.NewFlagRepository(pool), blobReader, postgres.NewExpungeRepository(pool), postgres.NewCopyRepository(pool))
	imapServer := imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return imappresentation.NewSession(c, imapUC)
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapMove: {}, imap.CapUIDPlus: {}},
		InsecureAuth: true,
	})
	imapLn, _ := net.Listen("tcp", "127.0.0.1:0")
	go imapServer.Serve(imapLn)
	defer imapServer.Close()

	webmailUC := appusecase.NewWebmailUseCase(postgres.NewWebmailRepository(pool), blobReader, submissionUC, authRepo, "mail.everywhere.test").
		WithLocalFiling(blobs, messages)
	router := httppresentation.NewRouter(nil, func() error { return nil })
	router.SetMailAPI(webmailUC, webmailSecret)
	web := httptest.NewServer(router)
	defer web.Close()
	token := mintSession(t, address, "user", time.Hour)

	// --------------------------------------------------------- small drivers
	webCall := func(method, path string, body any) []byte {
		t.Helper()
		var reader *bytes.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		} else {
			reader = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, web.URL+path, reader)
		req.Header.Set("Cookie", "lm_user_session="+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("webmail %s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		if resp.StatusCode >= 300 {
			t.Fatalf("webmail %s %s = %d: %s", method, path, resp.StatusCode, buf.String())
		}
		return buf.Bytes()
	}

	type webFolder struct {
		Name        string `json:"name"`
		UnreadCount int    `json:"unread_count"`
		TotalCount  int    `json:"total_count"`
	}
	webFolders := func() map[string]webFolder {
		t.Helper()
		var payload struct {
			Folders []webFolder `json:"folders"`
		}
		raw := webCall(http.MethodGet, "/api/v1/mail/folders", nil)
		if err := json.Unmarshal(raw, &payload); err != nil || payload.Folders == nil {
			var plain []webFolder
			if err2 := json.Unmarshal(raw, &plain); err2 != nil {
				t.Fatalf("decode folders: %v / %v: %s", err, err2, raw)
			}
			payload.Folders = plain
		}
		out := map[string]webFolder{}
		for _, f := range payload.Folders {
			out[f.Name] = f
		}
		return out
	}
	type webMessage struct {
		UID     uint32 `json:"uid"`
		Subject string `json:"subject"`
		Seen    bool   `json:"seen"`
	}
	webMessages := func(folder string) []webMessage {
		t.Helper()
		var payload struct {
			Messages []webMessage `json:"messages"`
		}
		raw := webCall(http.MethodGet, "/api/v1/mail/messages?folder="+folder, nil)
		if err := json.Unmarshal(raw, &payload); err != nil || payload.Messages == nil {
			var plain []webMessage
			if err2 := json.Unmarshal(raw, &plain); err2 != nil {
				t.Fatalf("decode messages: %v / %v: %s", err, err2, raw)
			}
			payload.Messages = plain
		}
		return payload.Messages
	}

	imapClient, err := imapclient.DialInsecure(imapLn.Addr().String(), nil)
	if err != nil {
		t.Fatalf("dial imap: %v", err)
	}
	defer imapClient.Close()
	if err := imapClient.Login(address, password).Wait(); err != nil {
		t.Fatalf("imap login: %v", err)
	}
	imapExists := func(folder string) int {
		t.Helper()
		data, err := imapClient.Select(folder, nil).Wait()
		if err != nil {
			t.Fatalf("imap select %s: %v", folder, err)
		}
		return int(data.NumMessages)
	}
	imapFlags := func(folder string) map[uint32][]imap.Flag {
		t.Helper()
		imapExists(folder)
		all := imap.UIDSet{}
		all.AddRange(1, 0) // 1:*
		cmd := imapClient.Fetch(all, &imap.FetchOptions{UID: true, Flags: true})
		out := map[uint32][]imap.Flag{}
		msgs, err := cmd.Collect()
		if err != nil {
			t.Fatalf("imap fetch flags in %s: %v", folder, err)
		}
		for _, m := range msgs {
			out[uint32(m.UID)] = m.Flags
		}
		return out
	}

	// agree compares what the webmail and IMAP each say about every folder, and
	// both with the rows: the one thing that must never differ
	agree := func(after string) {
		t.Helper()
		assertEverywhere := webFolders()
		for _, name := range []string{"INBOX", "Sent", "Trash", "Drafts"} {
			if got, want := imapExists(name), assertEverywhere[name].TotalCount; got != want {
				t.Errorf("after %s: %s holds %d messages over IMAP and %d in the webmail", after, name, got, want)
			}
		}
		var badFolders, badBytes int
		pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM (
			  SELECT f.id FROM folders f
			    LEFT JOIN email_messages e ON e.folder_id = f.id AND e.expunged_at IS NULL
			   WHERE f.mailbox_id = $1
			   GROUP BY f.id, f.total_count, f.unread_count
			  HAVING f.total_count <> COUNT(e.id)
			      OR f.unread_count <> COUNT(e.id) FILTER (WHERE NOT EXISTS (
			           SELECT 1 FROM message_flags mf WHERE mf.message_id = e.id AND mf.received_at = e.received_at AND mf.flag = '\Seen'))
			) x`, mailboxID).Scan(&badFolders)
		pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM mailboxes m
			 WHERE m.id = $1 AND m.used_bytes <> COALESCE((SELECT SUM(size_bytes) FROM email_messages WHERE mailbox_id = m.id AND expunged_at IS NULL), 0)`,
			mailboxID).Scan(&badBytes)
		if badFolders != 0 {
			t.Errorf("after %s: %d folder(s) have cached counters that differ from their messages", after, badFolders)
		}
		if badBytes != 0 {
			t.Errorf("after %s: the quota differs from the bytes actually stored", after)
		}
	}

	agree("delivery")
	if f := webFolders(); f["INBOX"].UnreadCount != 3 || f["INBOX"].TotalCount != 3 {
		t.Fatalf("INBOX should start with 3 unread of 3, the webmail says %+v", f["INBOX"])
	}

	// ----- 1. SMTP sends a message; Sent must show it in the webmail and over IMAP
	smtpClient, err := gosmtp.DialStartTLS(smtpLn.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("dial smtp: %v", err)
	}
	if err := smtpClient.Auth(plainAuth(address, password)); err != nil {
		t.Fatalf("smtp auth: %v", err)
	}
	smtpClient.Mail(address, nil)
	smtpClient.Rcpt("bob@remote.test", nil)
	w, _ := smtpClient.Data()
	w.Write([]byte("From: " + address + "\r\nTo: bob@remote.test\r\nSubject: sent over smtp\r\nMessage-ID: <smtp1@everywhere.test>\r\n\r\nhello\r\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("smtp data: %v", err)
	}
	smtpClient.Quit()

	agree("sending over SMTP")
	if sent := webMessages("sent"); len(sent) != 1 || sent[0].Subject != "sent over smtp" {
		t.Fatalf("the webmail's Sent should hold the SMTP message, got %+v", sent)
	}
	if n := imapExists("Sent"); n != 1 {
		t.Fatalf("IMAP's Sent holds %d messages after an SMTP send, want 1", n)
	}
	if f := webFolders(); f["Sent"].UnreadCount != 0 {
		t.Errorf("the sender's own copy shows as unread: %+v", f["Sent"])
	}

	// ----- 2. the webmail sends one too: it must land in the same place, once
	webCall(http.MethodPost, "/api/v1/mail/send", map[string]any{"to": []string{"carol@remote.test"}, "subject": "sent from webmail", "text": "hi"})
	agree("sending from the webmail")
	if n := imapExists("Sent"); n != 2 {
		t.Fatalf("Sent holds %d messages after one SMTP send and one webmail send, want exactly 2", n)
	}

	// ----- 3. read in the webmail; IMAP must see \Seen and the badge must fall
	inbox := webMessages("inbox")
	if len(inbox) != 3 {
		t.Fatalf("webmail lists %d INBOX messages, want 3", len(inbox))
	}
	uidA, uidB, uidC := inbox[2].UID, inbox[1].UID, inbox[0].UID // oldest first by UID
	webCall(http.MethodPost, "/api/v1/mail/seen", map[string]any{"folder": "inbox", "uid": uidA, "seen": true})
	agree("reading a message in the webmail")
	if flags := imapFlags("INBOX")[uidA]; !hasFlag(flags, imap.FlagSeen) {
		t.Errorf("IMAP does not see the message the webmail marked as read: %v", flags)
	}
	if f := webFolders(); f["INBOX"].UnreadCount != 2 {
		t.Errorf("INBOX unread = %d after one read, want 2", f["INBOX"].UnreadCount)
	}

	// ----- 4. read over IMAP; the webmail's badge must follow (it never did)
	imapExists("INBOX")
	if err := imapClient.Store(imap.UIDSetNum(imap.UID(uidB)), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
		t.Fatalf("imap store: %v", err)
	}
	agree("reading a message over IMAP")
	if f := webFolders(); f["INBOX"].UnreadCount != 1 {
		t.Errorf("INBOX unread = %d after a read over IMAP, want 1: the badge ignored it", f["INBOX"].UnreadCount)
	}
	for _, m := range webMessages("inbox") {
		if m.UID == uidB && !m.Seen {
			t.Error("the webmail lists the message IMAP marked as read as unread")
		}
	}

	// ----- 5. delete over IMAP (\Deleted + EXPUNGE); the webmail list shrinks, the quota is returned
	imapExists("INBOX")
	if err := imapClient.Store(imap.UIDSetNum(imap.UID(uidC)), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatalf("imap store deleted: %v", err)
	}
	if err := imapClient.Expunge().Close(); err != nil {
		t.Fatalf("imap expunge: %v", err)
	}
	agree("deleting over IMAP")
	if n := len(webMessages("inbox")); n != 2 {
		t.Errorf("the webmail lists %d INBOX messages after an IMAP delete, want 2", n)
	}

	// ----- 6. delete in the webmail (to Trash); IMAP sees it in Trash and not in INBOX
	webCall(http.MethodPost, "/api/v1/mail/delete", map[string]any{"folder": "inbox", "uid": uidA})
	agree("deleting in the webmail")
	if n := imapExists("Trash"); n != 1 {
		t.Errorf("IMAP's Trash holds %d messages, want 1", n)
	}
	if n := imapExists("INBOX"); n != 1 {
		t.Errorf("IMAP's INBOX holds %d messages, want 1", n)
	}

	// ----- 7. empty the Trash in the webmail: the bytes go back to the quota
	trash := webMessages("trash")
	if len(trash) != 1 {
		t.Fatalf("the webmail lists %d Trash messages, want 1", len(trash))
	}
	webCall(http.MethodPost, "/api/v1/mail/delete", map[string]any{"folder": "trash", "uid": trash[0].UID})
	agree("emptying the Trash")
	if n := imapExists("Trash"); n != 0 {
		t.Errorf("IMAP's Trash holds %d messages after emptying it, want 0", n)
	}
}

func hasFlag(flags []imap.Flag, want imap.Flag) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
