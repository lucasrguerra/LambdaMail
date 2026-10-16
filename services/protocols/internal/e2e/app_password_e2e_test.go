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

// TestSubmissionAcceptsAppPasswords drives the real submission server over
// STARTTLS. An app password is what a mail client uses on an account that has a
// second factor, and it used to be refused everywhere because the protocols
// only compared against the mailbox password.
func TestSubmissionAcceptsAppPasswords(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(15445).
		RuntimePath(t.TempDir()).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	pool, err := postgres.NewPool(ctx, "postgres://postgres:postgres@localhost:15445/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()
	applyMigrations(t, ctx, pool)

	const (
		address      = "plataforma@apppass.test"
		mainPassword = "the main mailbox password"
		sendSecret   = "app password that may send"
		readSecret   = "app password for reading only"
	)

	hashOf := func(secret string) string {
		hash, err := argon2id.CreateHash(secret, argon2id.DefaultParams)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		return hash
	}

	domainID, mailboxID := uuid.New(), uuid.New()
	pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'apppass.test', 'apppass.test')`, domainID)
	pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'plataforma', $3, $4)`,
		mailboxID, domainID, address, hashOf(mainPassword))
	// Default scopes: imap, pop3, smtp
	pool.Exec(ctx, `INSERT INTO app_passwords (mailbox_id, label, password_hash) VALUES ($1, 'ciberlab', $2)`, mailboxID, hashOf(sendSecret))
	pool.Exec(ctx, `INSERT INTO app_passwords (mailbox_id, label, password_hash, scopes) VALUES ($1, 'reader', $2, '{imap}')`, mailboxID, hashOf(readSecret))

	blobs := diskstorage.NewLocalDiskBlobStorage(pool, t.TempDir())
	submissionUC := appusecase.NewProcessOutboundEmailUseCase(postgres.NewAuthRepository(pool), postgres.NewOutboundRepository(pool), blobs, dkim.NewSigner(nil))

	server := gosmtp.NewServer(smtppresentation.NewSubmissionBackend(submissionUC))
	server.Domain = "mail.apppass.test"
	server.AllowInsecureAuth = false
	server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{selfSignedTLSCert(t)}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go server.Serve(ln)

	login := func(password string) error {
		client, err := gosmtp.DialStartTLS(ln.Addr().String(), &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer client.Close()
		return client.Auth(plainAuth(address, password))
	}

	if err := login(mainPassword); err != nil {
		t.Errorf("the mailbox password must keep working: %v", err)
	}
	if err := login(sendSecret); err != nil {
		t.Errorf("an app password with the smtp scope must sign in: %v", err)
	}
	if err := login(readSecret); err == nil {
		t.Error("an imap-only app password must not be able to submit mail")
	}
	if err := login("not a password"); err == nil {
		t.Error("a wrong password must still be refused")
	}

	var stamped bool
	pool.QueryRow(ctx, `SELECT last_used_at IS NOT NULL FROM app_passwords WHERE label = 'ciberlab'`).Scan(&stamped)
	if !stamped {
		t.Error("using an app password must stamp last_used_at")
	}
	var untouched bool
	pool.QueryRow(ctx, `SELECT last_used_at IS NULL FROM app_passwords WHERE label = 'reader'`).Scan(&untouched)
	if !untouched {
		t.Error("a refused app password must not be stamped as used")
	}
}
