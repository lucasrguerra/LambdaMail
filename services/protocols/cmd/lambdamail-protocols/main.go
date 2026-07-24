// Command lambdamail-protocols is the entry point for the protocols service.
package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	gosmtp "github.com/emersion/go-smtp"

	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/health"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	tlsprovider "lambdamail/protocols/internal/infrastructure/tls"
	imappresentation "lambdamail/protocols/internal/presentation/imap"
	smtppresentation "lambdamail/protocols/internal/presentation/smtp"
)

func main() {
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("open database pool: %v", err)
	}
	defer pool.Close()

	spoolDir := envOrDefault("PROTOCOLS_SPOOL_DIR", "/var/lambdamail/spool")
	if err := os.MkdirAll(spoolDir, 0o755); err != nil {
		log.Fatalf("create spool dir %s: %v", spoolDir, err)
	}

	mailboxes := postgres.NewMailboxRepository(pool)
	blobs := diskstorage.NewLocalDiskBlobStorage(pool, spoolDir)
	messages := postgres.NewInboundMessageRepository(pool)
	useCase := usecase.NewProcessInboundEmailUseCase(mailboxes, blobs, messages)

	certProvider, err := tlsprovider.NewLocalFileCertProvider(
		os.Getenv("PROTOCOLS_CERT_PATH"),
		os.Getenv("PROTOCOLS_KEY_PATH"),
	)
	if err != nil {
		log.Printf("WARNING: could not load configured TLS certificate (%v) - falling back to an ephemeral self-signed certificate; this is degraded mode, not suitable for real deployments", err)
		certProvider, err = tlsprovider.NewEphemeralSelfSignedCertProvider()
		if err != nil {
			log.Fatalf("generate fallback self-signed certificate: %v", err)
		}
	}

	backend := smtppresentation.NewBackend(useCase)
	smtpServer := gosmtp.NewServer(backend)
	smtpServer.Addr = ":25"
	smtpServer.Domain = envOrDefault("PRIMARY_MAIL_HOST", "mail.localhost")
	smtpServer.MaxMessageBytes = 52428800 // 50 MB default; per-domain limit is a future iteration
	smtpServer.MaxRecipients = 100
	smtpServer.TLSConfig = &tls.Config{
		GetCertificate: certProvider.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
	}

	go func() {
		log.Printf("lambdamail-protocols SMTP listening on %s", smtpServer.Addr)
		if err := smtpServer.ListenAndServe(); err != nil {
			log.Fatalf("smtp serve: %v", err)
		}
	}()

	authRepo := postgres.NewAuthRepository(pool)
	imapFolders := postgres.NewImapFolderRepository(pool)
	messageQuery := postgres.NewMessageQueryRepository(pool)
	flagRepo := postgres.NewFlagRepository(pool)
	blobReader := diskstorage.NewLocalDiskBlobReader(pool)
	expungeRepo := postgres.NewExpungeRepository(pool)
	copyRepo := postgres.NewCopyRepository(pool)
	imapUseCase := usecase.NewImapSessionUseCase(authRepo, imapFolders, messageQuery, flagRepo, blobReader, expungeRepo, copyRepo)

	imapServer := imapserver.New(&imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return imappresentation.NewSession(c, imapUseCase)
		},
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapMove:      {},
			imap.CapUIDPlus:   {},
		},
		TLSConfig:    &tls.Config{GetCertificate: certProvider.GetCertificate, MinVersion: tls.VersionTLS12},
		InsecureAuth: false,
	})

	go func() {
		log.Printf("lambdamail-protocols IMAP listening on :143")
		if err := imapServer.ListenAndServe(":143"); err != nil {
			log.Fatalf("imap serve: %v", err)
		}
	}()

	handler := health.Handler(func() error { return pool.Ping(ctx) })
	addr := ":8080"
	log.Printf("lambdamail-protocols health listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
