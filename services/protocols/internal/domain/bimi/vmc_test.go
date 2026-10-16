package bimi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

// The clock is pinned inside both fixtures' validity periods.
var fixtureNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func rules(violations []Violation) []string {
	out := make([]string, 0, len(violations))
	for _, v := range violations {
		out = append(out, v.Rule)
	}
	return out
}

// Real certificates, issued by DigiCert and through Valimail. These are the
// formats the parser has to read; a certificate built by hand would only prove
// the parser reads what it was written to read.
func TestInspectRealCertificates(t *testing.T) {
	cases := []struct {
		file, logo, domain, issuer string
	}{
		{"ebay.pem", "ebay.svg", "ebay.com", "DigiCert Verified Mark RSA4096 SHA256 2021 CA1"},
		{"cnn.pem", "cnn.svg", "cnn.com", ""},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			cert, violations := InspectCertificate(read(t, c.file), c.domain, read(t, c.logo), fixtureNow)
			if len(violations) != 0 {
				t.Fatalf("a genuine certificate was refused: %v", violations)
			}
			if !cert.MatchesLogo {
				t.Error("the logo embedded in the certificate should match the published one")
			}
			if cert.Kind != KindVMC {
				t.Errorf("kind = %q, want vmc (both are registered marks)", cert.Kind)
			}
			if cert.ChainLength < 2 {
				t.Errorf("chain length = %d, want the intermediates too", cert.ChainLength)
			}
			if c.issuer != "" && cert.Issuer != c.issuer {
				t.Errorf("issuer = %q", cert.Issuer)
			}
			if cert.Organization == "" || cert.NotAfter.IsZero() {
				t.Errorf("incomplete description: %+v", cert)
			}
		})
	}
}

func TestInspectCoversSubdomainsOfTheCertifiedDomain(t *testing.T) {
	// ebay.com's certificate names ebay.com, and mail from reply.ebay.com is covered
	_, violations := InspectCertificate(read(t, "ebay.pem"), "reply.ebay.com", read(t, "ebay.svg"), fixtureNow)
	if len(violations) != 0 {
		t.Fatalf("a subdomain of the certified domain was refused: %v", violations)
	}
}

func TestInspectRefusesTheWrongDomain(t *testing.T) {
	_, violations := InspectCertificate(read(t, "ebay.pem"), "example.test", read(t, "ebay.svg"), fixtureNow)
	if got := rules(violations); len(got) != 1 || got[0] != "domain" {
		t.Fatalf("violations = %v, want only the domain", got)
	}
}

func TestInspectRefusesADifferentLogo(t *testing.T) {
	// The certificate vouches for exactly one SVG. Anything else published at
	// l= makes the receiver ignore BIMI, so it is refused here instead.
	_, violations := InspectCertificate(read(t, "ebay.pem"), "ebay.com", read(t, "cnn.svg"), fixtureNow)
	if got := rules(violations); len(got) != 1 || got[0] != "logoMismatch" {
		t.Fatalf("violations = %v, want only the mismatch", got)
	}

	// Even one byte
	altered := append(read(t, "ebay.svg"), '\n')
	_, violations = InspectCertificate(read(t, "ebay.pem"), "ebay.com", altered, fixtureNow)
	if got := rules(violations); len(got) != 1 || got[0] != "logoMismatch" {
		t.Fatalf("a one-byte change must be a mismatch, got %v", got)
	}
}

func TestInspectChecksValidity(t *testing.T) {
	logo := read(t, "ebay.svg")
	_, violations := InspectCertificate(read(t, "ebay.pem"), "ebay.com", logo, time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC))
	if got := rules(violations); len(got) != 1 || got[0] != "validity" {
		t.Fatalf("an expired certificate: violations = %v", got)
	}
	_, violations = InspectCertificate(read(t, "ebay.pem"), "ebay.com", logo, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if got := rules(violations); len(got) != 1 || got[0] != "validity" {
		t.Fatalf("a certificate not yet valid: violations = %v", got)
	}

	// Close to expiry is allowed, with a warning
	cert, violations := InspectCertificate(read(t, "ebay.pem"), "ebay.com", logo, time.Date(2027, 7, 10, 0, 0, 0, 0, time.UTC))
	if len(violations) != 0 || len(cert.Warnings) == 0 {
		t.Fatalf("near expiry: violations=%v warnings=%v", violations, cert)
	}
}

func TestInspectNeverAcceptsAPrivateKey(t *testing.T) {
	// A key pasted next to the certificate would be published at a public URL
	key := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not a real key")})
	for _, payload := range [][]byte{key, append(read(t, "ebay.pem"), key...), append(key, read(t, "ebay.pem")...)} {
		cert, violations := InspectCertificate(payload, "ebay.com", read(t, "ebay.svg"), fixtureNow)
		if cert != nil || len(violations) != 1 || violations[0].Rule != "pemBlock" {
			t.Fatalf("a private key block was not refused: cert=%v violations=%v", cert, violations)
		}
		if strings.Contains(violations[0].Detail, "not a real key") {
			t.Error("the refusal must not echo the key")
		}
	}
}

func TestInspectRefusesGarbage(t *testing.T) {
	cases := map[string][]byte{
		"empty":   nil,
		"text":    []byte("this is not a certificate"),
		"corrupt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("garbage")}),
		"huge":    make([]byte, MaxCertificateBytes+1),
	}
	for name, payload := range cases {
		if cert, violations := InspectCertificate(payload, "ebay.com", read(t, "ebay.svg"), fixtureNow); cert != nil || len(violations) == 0 {
			t.Errorf("%s: cert=%v violations=%v", name, cert, violations)
		}
	}
}

// An ordinary web certificate is not a BIMI certificate: it has neither the
// brand-indicator usage nor a logo inside.
func TestInspectRefusesAnOrdinaryCertificate(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "example.test", Organization: []string{"Example"}},
		NotBefore:    fixtureNow.Add(-time.Hour),
		NotAfter:     fixtureNow.Add(365 * 24 * time.Hour),
		DNSNames:     []string{"example.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	payload := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	cert, violations := InspectCertificate(payload, "example.test", []byte("<svg/>"), fixtureNow)
	got := rules(violations)
	want := map[string]bool{"extendedKeyUsage": true, "logotype": true}
	if cert == nil || len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("violations = %v, want the missing usage and the missing logo", got)
	}
	if cert.MatchesLogo {
		t.Error("an ordinary certificate cannot match a logo")
	}
}

func TestInspectWarnsAboutALeafWithoutItsChain(t *testing.T) {
	first, _ := pem.Decode(read(t, "ebay.pem"))
	leafOnly := pem.EncodeToMemory(first)
	cert, violations := InspectCertificate(leafOnly, "ebay.com", read(t, "ebay.svg"), fixtureNow)
	if len(violations) != 0 {
		t.Fatalf("a leaf alone is usable, only worth a warning: %v", violations)
	}
	if len(cert.Warnings) == 0 || !strings.Contains(cert.Warnings[0], "intermediate") {
		t.Fatalf("warnings = %v", cert.Warnings)
	}
}

func TestInspectWarnsAboutAnOutOfOrderChain(t *testing.T) {
	var blocks []*pem.Block
	rest := read(t, "ebay.pem")
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		blocks = append(blocks, b)
	}
	// leaf first, then the chain reversed: each is no longer signed by the next
	swapped := pem.EncodeToMemory(blocks[0])
	for i := len(blocks) - 1; i >= 1; i-- {
		swapped = append(swapped, pem.EncodeToMemory(blocks[i])...)
	}
	cert, violations := InspectCertificate(swapped, "ebay.com", read(t, "ebay.svg"), fixtureNow)
	if len(violations) != 0 {
		t.Fatalf("violations = %v", violations)
	}
	found := false
	for _, w := range cert.Warnings {
		found = found || strings.Contains(w, "chain")
	}
	if !found {
		t.Fatalf("an out-of-order chain should warn: %v", cert.Warnings)
	}
}

func TestCoversDomain(t *testing.T) {
	cert := &x509.Certificate{DNSNames: []string{"example.test", "*.wild.test"}}
	for domain, want := range map[string]bool{
		"example.test":      true,
		"EXAMPLE.test":      true,
		"mail.example.test": true,
		"notexample.test":   false,
		"example.test.evil": false,
		"a.wild.test":       true,
		"wild.test":         false, // a wildcard never covers its own apex
		"other.test":        false,
	} {
		if got := coversDomain(cert, domain); got != want {
			t.Errorf("coversDomain(%q) = %v, want %v", domain, got, want)
		}
	}
}
