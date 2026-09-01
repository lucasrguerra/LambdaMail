package tlsprovider

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
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

// The DANE association an operator publishes has to be the hash of the key the
// server is actually serving. If the two ever drift, mail from a validating
// sender is refused outright - so the panel states the digest of the live
// certificate, which is the number to compare against the TLSA record.
func TestProviderStatus_ReportsTheSpkiDigestOfTheServedCertificate(t *testing.T) {
	provider, err := NewEphemeralSelfSignedCertProvider()
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	status := NewProviderStatus(provider)

	digest, ok := status.CertificateSpkiDigest("mail.example.test")
	if !ok {
		t.Fatal("no digest reported")
	}
	// TLSA 3 1 1 is a SHA-256 over the SubjectPublicKeyInfo: 64 hex characters.
	if len(digest) != 64 {
		t.Errorf("digest %q is %d characters, want 64", digest, len(digest))
	}
	for _, c := range digest {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("digest %q is not lowercase hex", digest)
		}
	}

	// It must be the SPKI hash, not a hash of the whole certificate: those are
	// different numbers and publishing the wrong one breaks delivery.
	leaf := status.leafFor("mail.example.test")
	want := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	if digest != hex.EncodeToString(want[:]) {
		t.Errorf("digest is not the SHA-256 of the SubjectPublicKeyInfo")
	}
}
