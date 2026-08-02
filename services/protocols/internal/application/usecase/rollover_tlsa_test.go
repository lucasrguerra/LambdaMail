package usecase

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func rolloverAt(t *testing.T, now time.Time) *RolloverTlsaUseCase {
	t.Helper()
	return NewRolloverTlsaUseCase(DefaultRolloverConfig()).
		WithClock(func() time.Time { return now })
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

var now = time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

// Far from expiry there is nothing to do, and exactly one association stays
// published.
func TestRollover_StableWhenRenewalIsFarAway(t *testing.T) {
	decision := rolloverAt(t, now).Decide(context.Background(), RolloverState{
		CurrentDigest: "aaaa",
		NotAfter:      now.Add(80 * 24 * time.Hour),
	})

	if decision.Stage != StageStable {
		t.Errorf("stage = %s, want STABLE", decision.Stage)
	}
	if len(decision.Publish) != 1 || decision.Publish[0] != "aaaa" {
		t.Errorf("Publish = %v, want just the current association", decision.Publish)
	}
}

// As renewal approaches, a next key has to be generated and published.
func TestRollover_StartsWhenRenewalApproaches(t *testing.T) {
	decision := rolloverAt(t, now).Decide(context.Background(), RolloverState{
		CurrentDigest: "aaaa",
		NotAfter:      now.Add(10 * 24 * time.Hour),
	})

	if decision.Stage != StagePublishNext {
		t.Errorf("stage = %s, want PUBLISH_NEXT", decision.Stage)
	}
}

// The critical invariant of PLAN.md section 5.1: while a rollover is in
// flight, BOTH associations stay published. Dropping the current one before
// the new certificate is live is exactly what breaks DANE delivery.
func TestRollover_PublishesBothAssociationsWhileInFlight(t *testing.T) {
	published := now.Add(-2 * time.Hour)

	decision := rolloverAt(t, now).Decide(context.Background(), RolloverState{
		CurrentDigest:      "aaaa",
		PendingDigest:      "bbbb",
		PendingPublishedAt: &published,
		NotAfter:           now.Add(10 * 24 * time.Hour),
	})

	if decision.Stage != StageAwaitPropagation {
		t.Errorf("stage = %s, want AWAIT_PROPAGATION", decision.Stage)
	}
	if len(decision.Publish) != 2 {
		t.Fatalf("Publish = %v, want both associations", decision.Publish)
	}
	if !contains(decision.Publish, "aaaa") || !contains(decision.Publish, "bbbb") {
		t.Errorf("Publish = %v, want the current and the next association", decision.Publish)
	}
}

// The certificate must not change before the new association has had time to
// propagate.
func TestRollover_WaitsOutPropagationBeforeRenewing(t *testing.T) {
	justPublished := now.Add(-30 * time.Minute)
	longPublished := now.Add(-30 * time.Hour)

	early := rolloverAt(t, now).Decide(context.Background(), RolloverState{
		CurrentDigest: "aaaa", PendingDigest: "bbbb", PendingPublishedAt: &justPublished,
	})
	if early.Stage != StageAwaitPropagation {
		t.Errorf("stage = %s, want AWAIT_PROPAGATION 30 minutes in", early.Stage)
	}

	ready := rolloverAt(t, now).Decide(context.Background(), RolloverState{
		CurrentDigest: "aaaa", PendingDigest: "bbbb", PendingPublishedAt: &longPublished,
	})
	if ready.Stage != StageRenew {
		t.Errorf("stage = %s, want RENEW after the propagation window", ready.Stage)
	}
	if len(ready.Publish) != 2 {
		t.Errorf("Publish = %v, both associations must remain during renewal", ready.Publish)
	}
}

// Once the new certificate is live, the old association is retired - but only
// after the caches holding it have expired.
func TestRollover_RetiresOldAssociationOnlyAfterPropagation(t *testing.T) {
	recent := now.Add(-2 * time.Hour)
	old := now.Add(-40 * time.Hour)

	cleanup := rolloverAt(t, now).Decide(context.Background(), RolloverState{
		CurrentDigest: "aaaa", PendingDigest: "bbbb",
		PendingPublishedAt: &recent, CertificateMatchesPending: true,
	})
	if cleanup.Stage != StageCleanup {
		t.Errorf("stage = %s, want CLEANUP", cleanup.Stage)
	}
	if len(cleanup.Publish) != 2 {
		t.Errorf("Publish = %v, the old association must survive the cache window", cleanup.Publish)
	}

	done := rolloverAt(t, now).Decide(context.Background(), RolloverState{
		CurrentDigest: "aaaa", PendingDigest: "bbbb",
		PendingPublishedAt: &old, CertificateMatchesPending: true,
	})
	if done.Stage != StageStable {
		t.Errorf("stage = %s, want STABLE", done.Stage)
	}
	if len(done.Publish) != 1 || done.Publish[0] != "bbbb" {
		t.Errorf("Publish = %v, want only the new association", done.Publish)
	}
}

// A pending key whose association has not been published yet must be
// published before anything else happens.
func TestRollover_PublishesUnpublishedPendingAssociation(t *testing.T) {
	decision := rolloverAt(t, now).Decide(context.Background(), RolloverState{
		CurrentDigest: "aaaa", PendingDigest: "bbbb", PendingPublishedAt: nil,
	})

	if decision.Stage != StagePublishNext {
		t.Errorf("stage = %s, want PUBLISH_NEXT", decision.Stage)
	}
	if !contains(decision.Publish, "aaaa") || !contains(decision.Publish, "bbbb") {
		t.Errorf("Publish = %v, want both", decision.Publish)
	}
}

func TestRollover_ShouldRenewNow(t *testing.T) {
	uc := rolloverAt(t, now)

	if uc.ShouldRenewNow(now.Add(60 * 24 * time.Hour)) {
		t.Error("a certificate valid for another 60 days should not be renewed")
	}
	if !uc.ShouldRenewNow(now.Add(10 * 24 * time.Hour)) {
		t.Error("a certificate expiring in 10 days should be renewed")
	}
	if !uc.ShouldRenewNow(time.Time{}) {
		t.Error("a missing certificate should be issued")
	}
}

// The digest must be derivable from the key alone. That is the property the
// whole rollover depends on: the association for the next certificate has to
// be publishable before that certificate is issued.
func TestTlsaDigestForKey_MatchesTheIssuedCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	fromKey, err := TlsaDigestForKey(keyPEM)
	if err != nil {
		t.Fatalf("TlsaDigestForKey: %v", err)
	}
	if len(fromKey) != 64 {
		t.Errorf("digest length = %d, want 64 hex characters for SHA-256", len(fromKey))
	}

	// Issue a certificate against that same key and confirm both agree.
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mail.example.test"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(90 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	fromCert, err := TlsaDigestForCertificate(certPEM)
	if err != nil {
		t.Fatalf("TlsaDigestForCertificate: %v", err)
	}

	if fromKey != fromCert {
		t.Errorf("digest from key = %s, from certificate = %s; they must agree or the pre-published association would never match", fromKey, fromCert)
	}
}

func TestTlsaDigest_RejectsGarbage(t *testing.T) {
	if _, err := TlsaDigestForKey([]byte("not pem")); err == nil {
		t.Error("expected an error for a non-PEM key")
	}
	if _, err := TlsaDigestForCertificate([]byte("not pem")); err == nil {
		t.Error("expected an error for a non-PEM certificate")
	}
}
