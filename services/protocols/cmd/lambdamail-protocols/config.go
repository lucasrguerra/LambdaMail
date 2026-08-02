package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// config is the resolved environment of PLAN.md section 13.
type config struct {
	DatabaseURL     string
	PrimaryMailHost string
	MailDomain      string
	SpoolDir        string

	JwtSecret string
	MasterKey string

	TLSMode           string
	TraefikAcmeDir    string
	TraefikAcmeFile   string
	CertPollInterval  time.Duration
	StaticCertPath    string
	StaticCertKeyPath string

	RspamdURL  string
	ClamavAddr string

	MtaStsMode      string
	OutboundDane    bool
	OutboundMtaSts  bool
	DnssecResolver  string
	MaxMessageBytes int64

	RelayHost string
	RelayPort int
	RelayUser string
	RelayPass string
	// RelaySpfInclude overrides the SPF mechanism published for the relay.
	// Providers document their own, and it is rarely the relay hostname's
	// organisational domain - see RelayConfig.SpfInclude.
	RelaySpfInclude string

	// ArcSealEnabled adds an ARC set to accepted messages. It is off by
	// default because it only earns its keep on a host that forwards mail
	// onward; sealing mail we are the final destination for changes the
	// stored message for no benefit (PLAN.md section 5).
	ArcSealEnabled bool

	// AcmeEmail and AcmeDirectoryURL configure mode B (PLAN.md section 8.3).
	AcmeEmail        string
	AcmeDirectoryURL string

	CloudflareToken string
	PublicIPv4      string
	PublicIPv6      string
	DnsSyncInterval time.Duration
}

func loadConfig() config {
	return config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		PrimaryMailHost: envOrDefault("PRIMARY_MAIL_HOST", "mail.localhost"),
		MailDomain:      os.Getenv("MAIL_DOMAIN"),
		SpoolDir:        envOrDefault("PROTOCOLS_SPOOL_DIR", "/var/lambdamail/spool"),

		MasterKey: os.Getenv("LAMBDAMAIL_MASTER_KEY"),
		// Shared with the auth service, which mints the webmail sessions this
		// service verifies on /api/v1/mail/*.
		JwtSecret: os.Getenv("JWT_SECRET"),

		TLSMode:           strings.ToLower(envOrDefault("TLS_MODE", "traefik")),
		TraefikAcmeDir:    os.Getenv("TRAEFIK_ACME_DIR"),
		TraefikAcmeFile:   envOrDefault("TRAEFIK_ACME_FILENAME", "acme.json"),
		CertPollInterval:  time.Duration(envInt("CERT_POLL_INTERVAL_SECONDS", 60)) * time.Second,
		StaticCertPath:    os.Getenv("PROTOCOLS_CERT_PATH"),
		StaticCertKeyPath: os.Getenv("PROTOCOLS_KEY_PATH"),

		RspamdURL:  os.Getenv("RSPAMD_URL"),
		ClamavAddr: os.Getenv("CLAMAV_ADDR"),

		MtaStsMode: envOrDefault("MTA_STS_MODE", "testing"),
		// DANE stays off unless explicitly enabled: PLAN.md section 5.1
		// explains why publishing TLSA under Traefik-managed certificates
		// breaks mail sixty days later.
		OutboundDane:    envBool("OUTBOUND_DANE_ENABLED", false),
		OutboundMtaSts:  envBool("OUTBOUND_MTA_STS_ENABLED", true),
		DnssecResolver:  envOrDefault("DNSSEC_RESOLVER", "1.1.1.1:53"),
		MaxMessageBytes: int64(envInt("MAX_MESSAGE_BYTES", 52428800)),

		RelayHost:       os.Getenv("RELAY_HOST"),
		RelayPort:       envInt("RELAY_PORT", 587),
		RelayUser:       os.Getenv("RELAY_USER"),
		RelayPass:       os.Getenv("RELAY_PASS"),
		RelaySpfInclude: os.Getenv("RELAY_SPF_INCLUDE"),

		ArcSealEnabled: envBool("ARC_SEAL_ENABLED", false),

		AcmeEmail:        os.Getenv("ACME_EMAIL"),
		AcmeDirectoryURL: os.Getenv("ACME_DIRECTORY_URL"),

		CloudflareToken: os.Getenv("CLOUDFLARE_API_TOKEN"),
		PublicIPv4:      os.Getenv("PUBLIC_IPV4"),
		PublicIPv6:      os.Getenv("PUBLIC_IPV6"),
		// PLAN.md section 7.5 reconciles every six hours to detect drift.
		DnsSyncInterval: time.Duration(envInt("DNS_SYNC_INTERVAL_SECONDS", 6*60*60)) * time.Second,
	}
}

// domainOfMailHost derives the mail domain from the mail host when it was not
// configured explicitly: "mail.example.com" yields "example.com".
func (c config) domain() string {
	if c.MailDomain != "" {
		return c.MailDomain
	}
	if _, rest, found := strings.Cut(c.PrimaryMailHost, "."); found {
		return rest
	}
	return c.PrimaryMailHost
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed
		}
	}
	return fallback
}
