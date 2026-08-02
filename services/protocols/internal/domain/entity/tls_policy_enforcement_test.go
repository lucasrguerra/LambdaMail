package entity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"lambdamail/protocols/internal/domain/valueobject"
)

func selfSignedCert(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mx.example.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

// PLAN.md section 5 / RFC 8461 section 2: DANE outranks MTA-STS.
func TestTLSPolicy_DaneTakesPrecedenceOverMtaSts(t *testing.T) {
	if got := NewTLSPolicy(true, true).Effective(); got != TLSModeDane {
		t.Errorf("Effective = %q, want dane", got)
	}
	if got := NewTLSPolicy(false, true).Effective(); got != TLSModeMtaSts {
		t.Errorf("Effective = %q, want mta-sts", got)
	}
	if got := NewTLSPolicy(false, false).Effective(); got != TLSModeOpportunistic {
		t.Errorf("Effective = %q, want opportunistic", got)
	}
}

// A destination publishing no policy must not be forced through validation;
// RFC 7435 prefers unauthenticated encryption to refusing the mail.
func TestTLSPolicy_OpportunisticDoesNotRequireValidation(t *testing.T) {
	if NewTLSPolicy(false, false).RequiresValidation() {
		t.Error("an opportunistic destination must not require validation")
	}
}

// "testing" mode exists precisely so that a broken TLS setup reports instead
// of losing mail (RFC 8461 section 5.2).
func TestTLSPolicy_MtaStsTestingDoesNotEnforce(t *testing.T) {
	testing_ := NewMtaStsPolicy([]string{"mx.example.test"}, false)
	if testing_.RequiresValidation() {
		t.Error("mode: testing must not enforce")
	}

	enforcing := NewMtaStsPolicy([]string{"mx.example.test"}, true)
	if !enforcing.RequiresValidation() {
		t.Error("mode: enforce must require validation")
	}
}

// DANE is authenticated by DNSSEC, so it always enforces (RFC 7672 section 2.2).
func TestTLSPolicy_DaneAlwaysEnforces(t *testing.T) {
	if !NewDanePolicy(nil).RequiresValidation() {
		t.Error("a DANE policy must always require validation")
	}
}

// RFC 8461 section 4.1: the wildcard covers exactly one label.
func TestTLSPolicy_CoversHostAppliesSingleLabelWildcard(t *testing.T) {
	policy := NewMtaStsPolicy([]string{"*.example.test", "mail.other.test"}, true)

	cases := map[string]bool{
		"mx1.example.test":     true,
		"MX1.EXAMPLE.TEST":     true,
		"mail.other.test":      true,
		"a.mx1.example.test":   false,
		"example.test":         false,
		"mx1.evil.test":        false,
		"mail.other.test.evil": false,
	}

	for host, want := range cases {
		if got := policy.CoversHost(host); got != want {
			t.Errorf("CoversHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// An opportunistic policy constrains no host.
func TestTLSPolicy_OpportunisticCoversEveryHost(t *testing.T) {
	if !NewTLSPolicy(false, false).CoversHost("anything.test") {
		t.Error("an opportunistic policy must not restrict the MX set")
	}
}

// A "3 1 1" association matches the SHA-256 of the SubjectPublicKeyInfo
// (RFC 6698 section 2.1.2).
func TestTLSPolicy_MatchesCertificateAgainstTlsaDigest(t *testing.T) {
	cert := selfSignedCert(t)
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)

	record, err := valueobject.NewTLSARecord(3, 1, 1, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("NewTLSARecord: %v", err)
	}

	policy := NewDanePolicy([]valueobject.TLSARecord{record})
	if !policy.MatchesCertificate([]*x509.Certificate{cert}) {
		t.Error("the certificate should match its own published digest")
	}

	// A different certificate must not match - this is the whole point.
	if policy.MatchesCertificate([]*x509.Certificate{selfSignedCert(t)}) {
		t.Error("an unrelated certificate matched the TLSA record")
	}
}

// During a rollover two associations are published; either one must satisfy
// the check (RFC 7671 section 8.1, PLAN.md section 5.1).
func TestTLSPolicy_MatchesEitherRolloverRecord(t *testing.T) {
	current, next := selfSignedCert(t), selfSignedCert(t)

	digest := func(c *x509.Certificate) string {
		sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
		return hex.EncodeToString(sum[:])
	}

	currentRecord, _ := valueobject.NewTLSARecord(3, 1, 1, digest(current))
	nextRecord, _ := valueobject.NewTLSARecord(3, 1, 1, digest(next))
	policy := NewDanePolicy([]valueobject.TLSARecord{currentRecord, nextRecord})

	if !policy.MatchesCertificate([]*x509.Certificate{current}) {
		t.Error("the current certificate must still validate during a rollover")
	}
	if !policy.MatchesCertificate([]*x509.Certificate{next}) {
		t.Error("the next certificate must already validate during a rollover")
	}
}

func TestTLSPolicy_MatchesCertificateRejectsEmptyInputs(t *testing.T) {
	record, _ := valueobject.NewTLSARecord(3, 1, 1, hex.EncodeToString(make([]byte, 32)))

	if NewDanePolicy([]valueobject.TLSARecord{record}).MatchesCertificate(nil) {
		t.Error("an empty chain must not validate")
	}
	if NewDanePolicy(nil).MatchesCertificate([]*x509.Certificate{selfSignedCert(t)}) {
		t.Error("a policy with no associations must not validate anything")
	}
}
