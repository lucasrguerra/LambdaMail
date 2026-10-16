package e2e

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/dkim"
	"lambdamail/protocols/internal/infrastructure/postgres"
	smtppresentation "lambdamail/protocols/internal/presentation/smtp"

	gosmtp "github.com/emersion/go-smtp"
)

// TestSubmissionFilesTheSentCopy drives the real submission server. A client
// that sends over SMTP used to leave nothing in the sender's Sent folder: the
// mail was delivered, but the account showed no sent mail at all, and a client
// cannot fill the folder itself because IMAP APPEND is not implemented.
func TestSubmissionFilesTheSentCopy(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(15450).
		RuntimePath(t.TempDir()).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	pool, err := postgres.NewPool(ctx, "postgres://postgres:postgres@localhost:15450/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()
	applyMigrations(t, ctx, pool)

	const (
		address  = "plataforma@sentcopy.test"
		password = "correct horse battery staple"
	)
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	domainID, mailboxID := uuid.New(), uuid.New()
	pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'sentcopy.test', 'sentcopy.test')`, domainID)
	pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'plataforma', $3, $4)`,
		mailboxID, domainID, address, hash)
	pool.Exec(ctx, `INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1, 'INBOX', 'inbox'), ($1, 'Sent', 'sent')`, mailboxID)

	blobs := diskstorage.NewLocalDiskBlobStorage(pool, t.TempDir())
	submissionUC := appusecase.NewProcessOutboundEmailUseCase(postgres.NewAuthRepository(pool), postgres.NewOutboundRepository(pool), blobs, dkim.NewSigner(nil))
	submissionUC.SetSentCopies(postgres.NewInboundMessageRepository(pool))

	server := gosmtp.NewServer(smtppresentation.NewSubmissionBackend(submissionUC))
	server.Domain = "mail.sentcopy.test"
	server.AllowInsecureAuth = false
	server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{selfSignedTLSCert(t)}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go server.Serve(ln)

	submitMessage(t, ln.Addr().String(), address, password, "ana@remote.test")
	submitMessage(t, ln.Addr().String(), address, password, "bruno@other.test")

	var sent int
	var subject string
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(MAX(m.subject), '')
		FROM email_messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.mailbox_id = $1 AND f.special_use = 'sent'`, mailboxID).Scan(&sent, &subject); err != nil {
		t.Fatalf("count Sent messages: %v", err)
	}

	if sent != 2 {
		t.Fatalf("Sent holds %d messages after two submissions, want 2", sent)
	}
	if subject != "e2e" {
		t.Errorf("Sent copy subject = %q, want the submitted one", subject)
	}

	var unread, total int
	if err := pool.QueryRow(ctx, `SELECT unread_count, total_count FROM folders WHERE mailbox_id = $1 AND special_use = 'sent'`, mailboxID).Scan(&unread, &total); err != nil {
		t.Fatalf("read Sent counters: %v", err)
	}
	if unread != 0 {
		t.Errorf("Sent advertises %d unread messages: the sender's own copies are not unread mail", unread)
	}
	if total != 2 {
		t.Errorf("Sent total_count = %d, want 2", total)
	}

	var inbox int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM email_messages m JOIN folders f ON f.id = m.folder_id
		WHERE m.mailbox_id = $1 AND f.special_use = 'inbox'`, mailboxID).Scan(&inbox); err != nil {
		t.Fatalf("count INBOX messages: %v", err)
	}
	if inbox != 0 {
		t.Errorf("INBOX holds %d messages, want 0: the copy belongs in Sent only", inbox)
	}

	// The delivery jobs and the Sent copy share one stored message per submission
	var blobs2, copies int
	if err := pool.QueryRow(ctx, `SELECT COUNT(DISTINCT blob_id) FROM outbound_jobs WHERE mailbox_id = $1`, mailboxID).Scan(&blobs2); err != nil {
		t.Fatalf("count queued blobs: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT m.blob_id) FROM email_messages m
		JOIN outbound_jobs j ON j.blob_id = m.blob_id WHERE m.mailbox_id = $1`, mailboxID).Scan(&copies); err != nil {
		t.Fatalf("count shared blobs: %v", err)
	}
	if copies != blobs2 {
		t.Errorf("the Sent copies reference %d of the %d queued messages' blobs, want all", copies, blobs2)
	}
}
