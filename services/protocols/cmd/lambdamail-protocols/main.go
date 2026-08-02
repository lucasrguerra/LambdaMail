// Command lambdamail-protocols is the entry point for the protocols service.
//
// Subcommands:
//
//	(none)       run every protocol listener
//	preflight    run the environment checks of PLAN.md section 15 and exit
//	healthcheck  probe the local HTTP health endpoint and exit
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	gosmtp "github.com/emersion/go-smtp"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/domain/valueobject"
	"lambdamail/protocols/internal/infrastructure/arc"
	"lambdamail/protocols/internal/infrastructure/clamav"
	"lambdamail/protocols/internal/infrastructure/cloudflare"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/dkim"
	"lambdamail/protocols/internal/infrastructure/mailauth"
	"lambdamail/protocols/internal/infrastructure/netdns"
	"lambdamail/protocols/internal/infrastructure/postgres"
	"lambdamail/protocols/internal/infrastructure/rspamd"
	tlsprovider "lambdamail/protocols/internal/infrastructure/tls"
	"lambdamail/protocols/internal/infrastructure/tlspolicy"
	"lambdamail/protocols/internal/infrastructure/vault"
	httppresentation "lambdamail/protocols/internal/presentation/http"
	imappresentation "lambdamail/protocols/internal/presentation/imap"
	managesievepresentation "lambdamail/protocols/internal/presentation/managesieve"
	pop3presentation "lambdamail/protocols/internal/presentation/pop3"
	smtppresentation "lambdamail/protocols/internal/presentation/smtp"
)

func main() {
	cfg := loadConfig()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "preflight":
			runPreflight(cfg)
			return
		case "healthcheck":
			runHealthcheck()
			return
		default:
			log.Fatalf("unknown subcommand %q (expected preflight or healthcheck)", os.Args[1])
		}
	}

	run(cfg)
}

func run(cfg config) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open database pool: %v", err)
	}
	defer pool.Close()

	if err := os.MkdirAll(cfg.SpoolDir, 0o755); err != nil {
		log.Fatalf("create spool dir %s: %v", cfg.SpoolDir, err)
	}

	secretVault, vaultErr := vault.New(cfg.MasterKey)
	certProvider, certDegraded := buildCertProvider(ctx, cfg, pool, secretVault, vaultErr)

	// ---------------------------------------------------------------- repos
	mailboxes := postgres.NewMailboxRepository(pool)
	blobs := diskstorage.NewLocalDiskBlobStorage(pool, cfg.SpoolDir)
	blobReader := diskstorage.NewLocalDiskBlobReader(pool)
	messages := postgres.NewInboundMessageRepository(pool)
	authRepo := postgres.NewAuthRepository(pool)
	imapFolders := postgres.NewImapFolderRepository(pool)
	messageQuery := postgres.NewMessageQueryRepository(pool)
	flagRepo := postgres.NewFlagRepository(pool)
	expungeRepo := postgres.NewExpungeRepository(pool)
	copyRepo := postgres.NewCopyRepository(pool)
	sieveRepo := postgres.NewSieveRepository(pool)
	reportRepo := postgres.NewReportRepository(pool)
	outboundRepo := postgres.NewOutboundRepository(pool)
	aliasRepo := postgres.NewAliasRepository(pool)

	// The vault is required for DKIM: without it the private keys could not
	// be stored, and unsigned outbound mail fails the DMARC alignment that
	// PLAN.md section 5 depends on.
	var dkimRepo *postgres.DkimRepository
	if vaultErr != nil {
		log.Printf("WARNING: %v - DKIM signing is disabled, outbound mail will not be signed", vaultErr)
	} else {
		dkimRepo = postgres.NewDkimRepository(pool, secretVault)
	}

	// ------------------------------------------------------------- inbound
	inboundUC := usecase.NewProcessInboundEmailUseCase(mailboxes, blobs, messages)
	inboundUC.SetAuthenticator(mailauth.NewAuthenticator(cfg.PrimaryMailHost))

	if scanner := buildScanner(cfg); scanner != nil {
		inboundUC.SetScanner(scanner)
	}

	// ------------------------------------------------------------ outbound
	var signer port.DkimSigner
	if dkimRepo != nil {
		signer = dkim.NewSigner(dkimRepo)
	}
	submissionUC := usecase.NewProcessOutboundEmailUseCase(authRepo, outboundRepo, blobs, signer)

	mxResolver := netdns.NewNetMXResolver()
	outboundWorker := usecase.NewOutboundWorkerUseCase(outboundRepo, mxResolver, blobReader, inboundUC, mailboxes, cfg.PrimaryMailHost)
	outboundWorker.SetPolicyResolver(buildPolicyResolver(cfg))
	if relay := relayConfig(cfg); relay.Configured() {
		outboundWorker.SetRelay(relay)
		log.Printf("lambdamail-protocols delivering through the smarthost %s", relay.Address())
	}

	// ARC needs a signing key, so it can only be enabled where DKIM is. The
	// key is resolved on the first message rather than here: on a first boot
	// the provisioner below has not run yet, and binding now would leave ARC
	// off until someone restarted the process.
	if cfg.ArcSealEnabled && dkimRepo != nil {
		inboundUC.SetArcSealer(arc.NewLazySealer(dkimRepo, dkim.ParsePrivateKey, cfg.domain(), cfg.PrimaryMailHost))
		log.Printf("lambdamail-protocols ARC sealing enabled for %s", cfg.domain())
	}

	// --------------------------------------------------------- access UCs
	imapUseCase := usecase.NewImapSessionUseCase(authRepo, imapFolders, messageQuery, flagRepo, blobReader, expungeRepo, copyRepo)
	inboundUC.SetTrackerManager(imapUseCase.GetTrackerManager(), imapFolders)
	pop3UseCase := usecase.NewPop3SessionUseCase(authRepo, imapFolders, messageQuery, blobReader, expungeRepo)
	sieveUseCase := usecase.NewManageSieveSessionUseCase(authRepo, sieveRepo)

	// ----------------------------------------------------------- listeners
	startSMTPListeners(cfg, certProvider, inboundUC, submissionUC)
	startIMAPListeners(cfg, certProvider, imapUseCase)
	startPOP3Listeners(certProvider, pop3UseCase)
	startManageSieveListener(certProvider, sieveUseCase)

	// ------------------------------------------------------- background jobs
	go runDeliveryWorker(ctx, outboundWorker)
	if cfg.CloudflareToken != "" {
		go runDnsReconciler(ctx, cfg, aliasRepo, dkimRepo)
	} else {
		log.Printf("CLOUDFLARE_API_TOKEN is not set: DNS reconciliation is disabled")
	}

	// ------------------------------------------------------------- HTTP API
	// The webmail reads and sends through this service because the folder,
	// UID, flag and blob logic already lives here; duplicating it in another
	// service would mean two implementations of the same IMAP semantics.
	webmailUC := usecase.NewWebmailUseCase(
		postgres.NewWebmailRepository(pool), blobReader, submissionUC, authRepo, cfg.PrimaryMailHost,
	)

	router := httppresentation.NewRouter(usecase.NewIngestReportsUseCase(reportRepo), func() error { return pool.Ping(ctx) })
	if cfg.JwtSecret == "" {
		log.Printf("JWT_SECRET is not set: the webmail mail API stays disabled")
	}
	router.SetMailAPI(webmailUC, cfg.JwtSecret)
	router.SetDegradedCheck(certDegraded)
	applyMtaStsMode(router, cfg)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("lambdamail-protocols HTTP service listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http serve: %v", err)
	}
	log.Printf("lambdamail-protocols shut down")
}

// buildCertProvider picks the certificate source. Mode "traefik" reads the
// store Traefik maintains and keeps watching it, because the certificate is
// replaced every sixty days and a stale one takes every listener down
// (PLAN.md section 8).
//
// The second return value is the probe behind /health's degraded state: a
// certificate problem does not stop the process, so it has to be visible
// somewhere an operator is already looking (PLAN.md section 8.4).
func buildCertProvider(ctx context.Context, cfg config, pool *pgxpool.Pool, secretVault *vault.SecretVault, vaultErr error) (port.CertProvider, func() error) {
	// Mode B: LambdaMail obtains the certificate itself. This is the only
	// mode DANE can run in, because it is the only one where the key for the
	// next certificate exists before that certificate does
	// (PLAN.md sections 5.1 and 8.3).
	if cfg.TLSMode == "acme" {
		if vaultErr != nil {
			log.Fatalf("TLS_MODE=acme needs LAMBDAMAIL_MASTER_KEY to seal the account and certificate keys: %v", vaultErr)
		}
		if cfg.CloudflareToken == "" {
			log.Fatalf("TLS_MODE=acme needs CLOUDFLARE_API_TOKEN: the DNS-01 challenge is answered through the Cloudflare API")
		}

		provider, err := startAcmeManager(ctx, cfg, pool, secretVault)
		if err != nil {
			log.Fatalf("start self-managed ACME: %v", err)
		}
		return provider, healthy
	}

	if cfg.TraefikAcmeDir != "" {
		watcher, err := tlsprovider.NewAcmeCertWatcher(cfg.TraefikAcmeDir, cfg.TraefikAcmeFile, cfg.PrimaryMailHost)
		if err == nil {
			if !watcher.HasCertificateFor(cfg.PrimaryMailHost) {
				log.Printf("CRITICAL: the ACME store has no certificate for %s - declare a Traefik router for that host so ACME issues one (PLAN.md section 8.2)", cfg.PrimaryMailHost)
			}
			go watcher.Watch(ctx.Done(), cfg.CertPollInterval)
			log.Printf("lambdamail-protocols reading TLS certificates from %s (polling every %s)", cfg.TraefikAcmeDir, cfg.CertPollInterval)
			return watcher, watcherDegradedCheck(watcher, cfg)
		}
		log.Printf("WARNING: could not read the ACME store (%v) - falling back", err)
	}

	if cfg.StaticCertPath != "" {
		if provider, err := tlsprovider.NewLocalFileCertProvider(cfg.StaticCertPath, cfg.StaticCertKeyPath); err == nil {
			return provider, healthy
		} else {
			log.Printf("WARNING: could not load the configured TLS certificate: %v", err)
		}
	}

	log.Printf("CRITICAL: falling back to an ephemeral self-signed certificate; this is degraded mode, not suitable for a real deployment")
	provider, err := tlsprovider.NewEphemeralSelfSignedCertProvider()
	if err != nil {
		log.Fatalf("generate fallback self-signed certificate: %v", err)
	}
	return provider, func() error {
		return errors.New("serving an ephemeral self-signed certificate: no ACME store could be read, so every client sees an untrusted certificate")
	}
}

// healthy is the degraded probe for a certificate source with nothing to
// report.
func healthy() error { return nil }

// watcherDegradedCheck turns the watcher's state into the conditions PLAN.md
// section 8.4 tabulates: a missing certificate for the mail host, a store that
// has stopped being re-read, and an expiry closing in.
func watcherDegradedCheck(watcher *tlsprovider.AcmeCertWatcher, cfg config) func() error {
	// Two missed polls is the smallest gap that cannot be a scheduling
	// hiccup, and it is the symptom of risk R2: a watcher gone blind.
	staleAfter := 3 * cfg.CertPollInterval

	return func() error {
		if !watcher.HasCertificateFor(cfg.PrimaryMailHost) {
			return fmt.Errorf("the ACME store holds no certificate for %s: declare a Traefik router for that host", cfg.PrimaryMailHost)
		}
		if last := watcher.LastReload(); !last.IsZero() && time.Since(last) > staleAfter {
			return fmt.Errorf("the ACME store has not been re-read since %s", last.Format(time.RFC3339))
		}
		if host, expiry, ok := watcher.EarliestExpiry(); ok && time.Until(expiry) < 24*time.Hour {
			return fmt.Errorf("the certificate for %s expires at %s", host, expiry.Format(time.RFC3339))
		}
		return nil
	}
}

func relayConfig(cfg config) usecase.RelayConfig {
	return usecase.RelayConfig{
		Host:     cfg.RelayHost,
		Port:     cfg.RelayPort,
		Username: cfg.RelayUser,
		Password: cfg.RelayPass,
	}
}

func buildScanner(cfg config) port.ContentScanner {
	var scanners []port.ContentScanner
	if cfg.ClamavAddr != "" {
		scanners = append(scanners, clamav.NewClamAVAdapter(cfg.ClamavAddr))
	}
	if cfg.RspamdURL != "" {
		scanners = append(scanners, rspamd.NewRspamdAdapter(cfg.RspamdURL))
	}
	if len(scanners) == 0 {
		return nil
	}
	log.Printf("lambdamail-protocols content scanning enabled (clamav: %q, rspamd: %q)", cfg.ClamavAddr, cfg.RspamdURL)
	return usecase.NewScanningPipeline(scanners...)
}

// buildPolicyResolver assembles the outbound transport security policy of
// PLAN.md section 6.2.
func buildPolicyResolver(cfg config) port.TLSPolicyResolver {
	// With a smarthost the relay takes responsibility for transport security
	// (PLAN.md section 10.4), so applying DANE and MTA-STS here would only
	// evaluate policies against the wrong hop.
	if cfg.RelayHost != "" {
		log.Printf("lambdamail-protocols relaying through %s: DANE and MTA-STS are handled by the relay", cfg.RelayHost)
		return tlspolicy.NewResolver(tlspolicy.WithMtaSts(false), tlspolicy.WithDane(false, nil))
	}

	options := []tlspolicy.Option{tlspolicy.WithMtaSts(cfg.OutboundMtaSts)}
	if cfg.OutboundDane {
		options = append(options, tlspolicy.WithDane(true, netdns.NewTlsaResolver(cfg.DnssecResolver)))
	}

	log.Printf("lambdamail-protocols outbound TLS policy: dane=%v mta-sts=%v", cfg.OutboundDane, cfg.OutboundMtaSts)
	return tlspolicy.NewResolver(options...)
}

func applyMtaStsMode(router *httppresentation.Router, cfg config) {
	mode := valueobject.MtaStsMode(cfg.MtaStsMode)
	switch mode {
	case valueobject.MtaStsModeTesting, valueobject.MtaStsModeEnforce, valueobject.MtaStsModeNone:
		router.SetMtaStsMode(mode)
		log.Printf("lambdamail-protocols MTA-STS policy mode: %s", mode)
	default:
		log.Printf("WARNING: ignoring unknown MTA_STS_MODE %q, serving %q", mode, valueobject.MtaStsModeTesting)
	}
}

// startSMTPListeners brings up the three SMTP ports of PLAN.md section 4. The
// MX port and the submission ports run different backends: port 25 never
// authenticates and only accepts local mail, while submission always
// authenticates and relays outward.
func startSMTPListeners(cfg config, certProvider port.CertProvider, inboundUC *usecase.ProcessInboundEmailUseCase, submissionUC *usecase.ProcessOutboundEmailUseCase) {
	tlsConfig := mailTLSConfig(certProvider)

	// Port 25: STARTTLS is always advertised but never required. RFC 5321
	// obliges an MX to accept cleartext, and refusing it loses legitimate
	// mail without improving any audit score (PLAN.md section 4).
	mx := gosmtp.NewServer(smtppresentation.NewBackend(inboundUC))
	mx.Addr = ":25"
	mx.Domain = cfg.PrimaryMailHost
	mx.MaxMessageBytes = cfg.MaxMessageBytes
	mx.MaxRecipients = 100
	mx.ReadTimeout = 5 * time.Minute
	mx.WriteTimeout = 5 * time.Minute
	mx.TLSConfig = tlsConfig
	serveInBackground("SMTP MX", ":25", mx.ListenAndServe)

	// Port 587: submission over STARTTLS. AllowInsecureAuth stays false, so
	// go-smtp withholds the AUTH capability until the connection is
	// encrypted - credentials never travel in the clear.
	submission := gosmtp.NewServer(smtppresentation.NewSubmissionBackend(submissionUC))
	submission.Addr = ":587"
	submission.Domain = cfg.PrimaryMailHost
	submission.MaxMessageBytes = cfg.MaxMessageBytes
	submission.MaxRecipients = 100
	submission.ReadTimeout = 5 * time.Minute
	submission.WriteTimeout = 5 * time.Minute
	submission.TLSConfig = tlsConfig
	submission.AllowInsecureAuth = false
	serveInBackground("SMTP submission", ":587", submission.ListenAndServe)

	// Port 465: implicit TLS (RFC 8314), the recommended submission port.
	submissions := gosmtp.NewServer(smtppresentation.NewSubmissionBackend(submissionUC))
	submissions.Addr = ":465"
	submissions.Domain = cfg.PrimaryMailHost
	submissions.MaxMessageBytes = cfg.MaxMessageBytes
	submissions.MaxRecipients = 100
	submissions.ReadTimeout = 5 * time.Minute
	submissions.WriteTimeout = 5 * time.Minute
	submissions.TLSConfig = tlsConfig
	serveInBackground("SMTPS submission", ":465", submissions.ListenAndServeTLS)
}

func startIMAPListeners(cfg config, certProvider port.CertProvider, imapUseCase *usecase.ImapSessionUseCase) {
	newServer := func() *imapserver.Server {
		return imapserver.New(&imapserver.Options{
			NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
				return imappresentation.NewSession(c, imapUseCase)
			},
			Caps: imap.CapSet{
				imap.CapIMAP4rev1: {},
				imap.CapMove:      {},
				imap.CapUIDPlus:   {},
				imap.CapIdle:      {},
				imap.CapCondStore: {},
			},
			TLSConfig: mailTLSConfig(certProvider),
			// LOGINDISABLED is advertised before TLS: the client must
			// upgrade before it may send credentials (PLAN.md section 4).
			InsecureAuth: false,
		})
	}

	serveInBackground("IMAP", ":143", func() error { return newServer().ListenAndServe(":143") })
	serveInBackground("IMAPS", ":993", func() error {
		return serveImplicitTLS(":993", mailTLSConfig(certProvider), newServer().Serve)
	})
}

func startPOP3Listeners(certProvider port.CertProvider, pop3UseCase *usecase.Pop3SessionUseCase) {
	serveInBackground("POP3", ":110", pop3presentation.NewServer(":110", pop3UseCase, certProvider).ListenAndServe)
	serveInBackground("POP3S", ":995", func() error {
		server := pop3presentation.NewServer(":995", pop3UseCase, certProvider)
		return serveImplicitTLS(":995", mailTLSConfig(certProvider), server.Serve)
	})
}

func startManageSieveListener(certProvider port.CertProvider, sieveUseCase *usecase.ManageSieveSessionUseCase) {
	serveInBackground("ManageSieve", ":4190", managesievepresentation.NewServer(":4190", sieveUseCase, certProvider).ListenAndServe)
}

// serveImplicitTLS wraps a plain listener in TLS, which is what the implicit
// ports (465, 993, 995) need: the handshake happens before any protocol
// greeting rather than after a STARTTLS command.
func serveImplicitTLS(addr string, tlsConfig *tls.Config, serve func(net.Listener) error) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return serve(tls.NewListener(ln, tlsConfig))
}

// serveInBackground runs a listener and logs a fatal error if it dies. A
// protocol port that silently fails to bind would look healthy while being
// unreachable, so failure is loud.
func serveInBackground(name, addr string, serve func() error) {
	go func() {
		log.Printf("lambdamail-protocols %s listening on %s", name, addr)
		if err := serve(); err != nil {
			log.Fatalf("%s serve on %s: %v", name, addr, err)
		}
	}()
}

// mailTLSConfig is the cipher policy of PLAN.md section 4: TLS 1.2 floor,
// forward secrecy only, and the three curves named there.
func mailTLSConfig(certProvider port.CertProvider) *tls.Config {
	return &tls.Config{
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
}

func runDeliveryWorker(ctx context.Context, worker *usecase.OutboundWorkerUseCase) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := worker.ProcessBatch(ctx, "protocols-worker-1", 10); err != nil {
				log.Printf("delivery worker batch failed: %v", err)
			}
		}
	}
}

// runDnsReconciler provisions the DKIM keys and reconciles the zone, then
// repeats on the interval of PLAN.md section 7.5 to detect drift.
func runDnsReconciler(ctx context.Context, cfg config, aliasRepo port.SystemAliasRepository, dkimRepo *postgres.DkimRepository) {
	syncUC := usecase.NewSyncDnsRecordsUseCase(cloudflare.NewCloudflareAdapter(cfg.CloudflareToken), aliasRepo)
	syncUC.SetVerifier(netdns.NewPublicVerifier())

	var provisioner *usecase.ProvisionDkimKeysUseCase
	if dkimRepo != nil {
		provisioner = usecase.NewProvisionDkimKeysUseCase(dkimRepo, generateDkimKey)
	}

	reconcile := func() {
		domain := cfg.domain()

		input := usecase.SyncDnsRecordsInput{
			DomainName:  domain,
			MailHost:    cfg.PrimaryMailHost,
			ServerIPv4:  cfg.PublicIPv4,
			ServerIPv6:  cfg.PublicIPv6,
			DaneEnabled: cfg.OutboundDane,
			// A smarthost sends on the domain's behalf, so the hard-fail SPF
			// has to authorise it or every relayed message fails
			// (PLAN.md section 10.4).
			RelaySpfInclude: relayConfig(cfg).SpfInclude(),
		}

		if provisioner != nil {
			keys, err := provisioner.Execute(ctx, domain)
			if err != nil {
				log.Printf("DNS reconcile: could not provision DKIM keys for %s: %v", domain, err)
				return
			}
			input.RsaDkimPubKey = keys.RsaPublicKey
			input.EdDkimPubKey = keys.Ed25519PublicKey
			if len(keys.Created) > 0 {
				log.Printf("DNS reconcile: generated DKIM keys for %s: %v", domain, keys.Created)
			}
		}

		out, err := syncUC.Execute(ctx, input)
		if err != nil {
			log.Printf("DNS reconcile for %s failed: %v", domain, err)
			return
		}

		log.Printf("DNS reconcile for %s: %s (created=%d updated=%d unchanged=%d conflicts=%d)",
			domain, out.Status, out.CreatedCount, out.UpdatedCount, out.UnchangedCount, out.ConflictCount)

		// Conflicts and unresolvable records are reported individually
		// because each needs a different operator action.
		for _, conflict := range out.Conflicts {
			log.Printf("DNS conflict (left untouched): %s", conflict)
		}
		for _, unverified := range out.Unverified {
			log.Printf("DNS record not yet visible to public resolvers: %s", unverified)
		}
		for _, e := range out.Errors {
			log.Printf("DNS reconcile error: %s", e)
		}
	}

	reconcile()

	ticker := time.NewTicker(cfg.DnsSyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

// generateDkimKey adapts the key generator to the use case's signature,
// keeping the crypto in the infrastructure layer.
func generateDkimKey(algorithm string) ([]byte, string, error) {
	generated, err := dkim.Generate(algorithm)
	if err != nil {
		return nil, "", err
	}
	return generated.PrivateKeyPEM, generated.PublicKeyBase64, nil
}

func runHealthcheck() {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8080/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck failed: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
