package tlsprovider

import (
	"testing"
	"time"
)

// The TLS panel answered 503 "the TLS panel needs a certificate watcher" on a
// real deployment, because the panel was only wired when the certificate came
// from the Traefik acme.json watcher. With TRAEFIK_ACME_DIR unset the server
// falls back to an ephemeral self-signed certificate - which is exactly the
// state an operator most needs the panel to show, and precisely the one it
// refused to report.
func TestProviderStatus_ReportsAnEphemeralSelfSignedCertificate(t *testing.T) {
	provider, err := NewEphemeralSelfSignedCertProvider()
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	status := NewProviderStatus(provider)

	if !status.HasCertificateFor("mail.example.test") {
		t.Error("reported no certificate at all; the server is serving one")
	}

	host, expiry, ok := status.EarliestExpiry()
	if !ok {
		t.Fatal("could not read the expiry, so the panel would show nothing")
	}
	if expiry.Before(time.Now()) {
		t.Errorf("expiry %v is in the past", expiry)
	}
	if host == "" {
		t.Error("no host named for the expiring certificate")
	}

	// The whole point: an operator must be able to see this is not a real
	// certificate. Serving self-signed on port 25 and 993 means every client
	// that verifies is refusing the connection.
	issuer, selfSigned, ok := status.CertificateSummary("mail.example.test")
	if !ok {
		t.Fatal("no summary available")
	}
	if !selfSigned {
		t.Error("an ephemeral self-signed certificate was not reported as self-signed")
	}
	if issuer == "" {
		t.Error("no issuer reported")
	}
}
