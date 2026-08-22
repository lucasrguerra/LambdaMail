package e2e

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/dkim"
	"lambdamail/protocols/internal/infrastructure/postgres"
	"lambdamail/protocols/internal/infrastructure/vault"
	smtppresentation "lambdamail/protocols/internal/presentation/smtp"

	gosmtp "github.com/emersion/go-smtp"
)

// TestSubmissionEndToEnd covers the outbound half of PLAN.md section 6.2:
// an authenticated client submits on the submission port, the message is
// signed with both DKIM keys, queued, and then delivered by the worker to a
// remote MX.
func TestSubmissionEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(15444).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:15444/postgres?sslmode=disable"
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	applyMigrations(t, ctx, pool)

	// ---------------------------------------------------------------- seed
	const (
		senderAddress = "sender@submission.test"
		password      = "correct horse battery staple"
	)

	domainID, mailboxID, inboxID := uuid.New(), uuid.New(), uuid.New()
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'submission.test', 'submission.test')`, domainID)
	pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, max_recipients_per_hour)
		VALUES ($1, $2, 'sender', $3, $4, 5)`, mailboxID, domainID, senderAddress, hash)
	pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`, inboxID, mailboxID)

	// ----------------------------------------------------------- provision
	secretVault, err := vault.New("e2e-master-key")
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	dkimRepo := postgres.NewDkimRepository(pool, secretVault)

	provisioner := appusecase.NewProvisionDkimKeysUseCase(dkimRepo, func(algorithm string) ([]byte, string, error) {
		generated, err := dkim.Generate(algorithm)
		if err != nil {
			return nil, "", err
		}
		return generated.PrivateKeyPEM, generated.PublicKeyBase64, nil
	})

	keys, err := provisioner.Execute(ctx, "submission.test")
	if err != nil {
		t.Fatalf("provision DKIM keys: %v", err)
	}
	if keys.RsaPublicKey == "" || keys.Ed25519PublicKey == "" {
		t.Fatal("provisioning produced no public keys for the DNS records")
	}

	// The private halves must not be readable from the table without the
	// master key (PLAN.md section 9).
	var storedCiphertext []byte
	pool.QueryRow(ctx, `SELECT private_key_enc FROM dkim_keys WHERE algorithm = 'rsa2048'`).Scan(&storedCiphertext)
	if strings.Contains(string(storedCiphertext), "PRIVATE KEY") {
		t.Error("the DKIM private key was stored in the clear")
	}

	// ------------------------------------------------------------- wiring
	blobs := diskstorage.NewLocalDiskBlobStorage(pool, t.TempDir())
	blobReader := diskstorage.NewLocalDiskBlobReader(pool)
	authRepo := postgres.NewAuthRepository(pool)
	outboundRepo := postgres.NewOutboundRepository(pool)

	submissionUC := appusecase.NewProcessOutboundEmailUseCase(authRepo, outboundRepo, blobs, dkim.NewSigner(dkimRepo))

	certificate := selfSignedTLSCert(t)
	server := gosmtp.NewServer(smtppresentation.NewSubmissionBackend(submissionUC))
	server.Domain = "mail.submission.test"
	server.MaxMessageBytes = 10 << 20
	server.MaxRecipients = 10
	server.AllowInsecureAuth = false
	server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go server.Serve(ln)

	// -------------------------------------------------- submit over STARTTLS
	submitMessage(t, ln.Addr().String(), senderAddress, password, "rcpt@remote.test")

	// The job must be queued against the sending mailbox.
	var queued int
	var destinationDomain string
	pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(MAX(destination_domain), '') FROM outbound_jobs WHERE mailbox_id = $1`, mailboxID).
		Scan(&queued, &destinationDomain)
	if queued != 1 {
		t.Fatalf("queued %d jobs, want 1", queued)
	}
	if destinationDomain != "remote.test" {
		t.Errorf("destination domain = %q, want remote.test", destinationDomain)
	}

	// The stored message must carry both signatures.
	var blobID uuid.UUID
	pool.QueryRow(ctx, `SELECT blob_id FROM outbound_jobs WHERE mailbox_id = $1`, mailboxID).Scan(&blobID)
	stored, err := blobReader.ReadByID(ctx, blobID)
	if err != nil {
		t.Fatalf("read stored message: %v", err)
	}
	if count := strings.Count(string(stored), "DKIM-Signature:"); count != 2 {
		t.Errorf("stored message carries %d DKIM signatures, want 2 (RSA and Ed25519)", count)
	}
	if !strings.Contains(string(stored), "d=submission.test") {
		t.Errorf("signatures do not claim the sending domain:\n%s", stored)
	}

	// --------------------------------------------------------- delivery
	received := make(chan string, 1)
	mxAddr := startCapturingMX(t, received)

	worker := appusecase.NewOutboundWorkerUseCase(
		outboundRepo, &staticMXResolver{addr: mxAddr}, blobReader, nil, nil, "mail.submission.test",
	)
	if _, err := worker.ProcessBatch(ctx, "worker1", 10); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	var status, tlsPolicy string
	pool.QueryRow(ctx, `SELECT status, COALESCE(tls_policy_used, '') FROM outbound_jobs WHERE mailbox_id = $1`, mailboxID).
		Scan(&status, &tlsPolicy)
	if status != "DELIVERED" {
		var lastError string
		pool.QueryRow(ctx, `SELECT COALESCE(last_error, '') FROM outbound_jobs WHERE mailbox_id = $1`, mailboxID).Scan(&lastError)
		t.Fatalf("status = %s, want DELIVERED (last error: %s)", status, lastError)
	}
	if tlsPolicy != "opportunistic" {
		t.Errorf("tls_policy_used = %q, want opportunistic for a destination with no published policy", tlsPolicy)
	}

	select {
	case delivered := <-received:
		if !strings.Contains(delivered, "DKIM-Signature:") {
			t.Error("the delivered message lost its signatures")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the remote MX never received the message")
	}

	// ------------------------------------------------ per-mailbox send limit
	// The mailbox allows five recipients an hour and has used one.
	assertSubmissionRefused(t, ln.Addr().String(), senderAddress, password,
		[]string{"a@remote.test", "b@remote.test", "c@remote.test", "d@remote.test", "e@remote.test"})

	// An account may not send as somebody else.
	assertSenderRefused(t, ln.Addr().String(), senderAddress, password, "ceo@submission.test")
}

// submitMessage performs a full EHLO/STARTTLS/AUTH/MAIL/RCPT/DATA exchange.
func submitMessage(t *testing.T, addr, username, password, recipient string) {
	t.Helper()

	client, err := gosmtp.DialStartTLS(addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("dial submission over STARTTLS: %v", err)
	}
	defer client.Close()

	if err := client.Auth(plainAuth(username, password)); err != nil {
		t.Fatalf("AUTH: %v", err)
	}
	if err := client.Mail(username, nil); err != nil {
		t.Fatalf("MAIL FROM: %v", err)
	}
	if err := client.Rcpt(recipient, nil); err != nil {
		t.Fatalf("RCPT TO: %v", err)
	}

	w, err := client.Data()
	if err != nil {
		t.Fatalf("DATA: %v", err)
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: e2e\r\nDate: Mon, 20 Jul 2026 10:00:00 +0000\r\nMessage-ID: <e2e@submission.test>\r\n\r\nhello\r\n", username, recipient)
	if _, err := w.Write([]byte(message)); err != nil {
		t.Fatalf("write DATA: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close DATA: %v", err)
	}
	client.Quit()
}

// assertSubmissionRefused checks that going past the hourly recipient budget
// is refused with a temporary error rather than silently accepted.
func assertSubmissionRefused(t *testing.T, addr, username, password string, recipients []string) {
	t.Helper()

	client, err := gosmtp.DialStartTLS(addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	client.Auth(plainAuth(username, password))
	client.Mail(username, nil)
	for _, r := range recipients {
		client.Rcpt(r, nil)
	}

	w, err := client.Data()
	if err != nil {
		// Some servers refuse at DATA issue time; either point is acceptable.
		return
	}
	w.Write([]byte("From: x\r\n\r\nbody\r\n"))
	if err := w.Close(); err == nil {
		t.Error("submission past the hourly recipient limit was accepted")
	} else if !strings.Contains(err.Error(), "451") {
		t.Errorf("error = %v, want a 451 temporary refusal", err)
	}
}

func assertSenderRefused(t *testing.T, addr, username, password, forgedSender string) {
	t.Helper()

	client, err := gosmtp.DialStartTLS(addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	client.Auth(plainAuth(username, password))

	if err := client.Mail(forgedSender, nil); err == nil {
		t.Errorf("MAIL FROM %s was accepted for an account that does not own it", forgedSender)
	}
}

// startCapturingMX accepts one message and hands its body back on a channel.
func startCapturingMX(t *testing.T, received chan<- string) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(10 * time.Second))
				r := bufio.NewReader(c)
				c.Write([]byte("220 capture.mx ESMTP\r\n"))

				var body strings.Builder
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						c.Write([]byte("250 capture.mx\r\n"))
					case cmd == "DATA":
						c.Write([]byte("354 Send it\r\n"))
						for {
							dataLine, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if strings.TrimSpace(dataLine) == "." {
								break
							}
							body.WriteString(dataLine)
						}
						c.Write([]byte("250 2.0.0 Queued\r\n"))
						select {
						case received <- body.String():
						default:
						}
					case cmd == "QUIT":
						c.Write([]byte("221 Bye\r\n"))
						return
					default:
						c.Write([]byte("250 Ok\r\n"))
					}
				}
			}(conn)
		}
	}()

	return ln.Addr().String()
}

// plainAuth builds a SASL PLAIN client without pulling in another dependency.
type plainAuthClient struct {
	username, password string
	done               bool
}

func plainAuth(username, password string) *plainAuthClient {
	return &plainAuthClient{username: username, password: password}
}

func (a *plainAuthClient) Start() (mech string, ir []byte, err error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a *plainAuthClient) Next(challenge []byte) ([]byte, error) {
	if a.done {
		return nil, fmt.Errorf("unexpected challenge %q", base64.StdEncoding.EncodeToString(challenge))
	}
	a.done = true
	return nil, nil
}
