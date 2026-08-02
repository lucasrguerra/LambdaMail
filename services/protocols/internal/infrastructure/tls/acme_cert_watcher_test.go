package tlsprovider

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeCertPEM issues a self-signed certificate for the given names.
func makeCertPEM(t *testing.T, names ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	return makeCertPEMWithValidity(t, -time.Hour, 90*24*time.Hour, names...)
}

// makeCertPEMWithValidity issues a certificate whose validity window is given
// relative to now, so a test can produce an expired or nearly expired one.
func makeCertPEMWithValidity(t *testing.T, notBefore, notAfter time.Duration, names ...string) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    time.Now().Add(notBefore),
		NotAfter:     time.Now().Add(notAfter),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// writeAcmeStore builds an acme.json in the shape Traefik writes: a map of
// resolver name to that resolver's account and certificates.
func writeAcmeStore(t *testing.T, dir, resolverName string, entries map[string][]string) string {
	t.Helper()
	return writeAcmeStoreWithValidity(t, dir, resolverName, entries, -time.Hour, 90*24*time.Hour)
}

// writeAcmeStoreWithValidity is writeAcmeStore with control over how long the
// issued certificates are valid for.
func writeAcmeStoreWithValidity(t *testing.T, dir, resolverName string, entries map[string][]string, notBefore, notAfter time.Duration) string {
	t.Helper()

	type domain struct {
		Main string   `json:"main"`
		SANs []string `json:"sans"`
	}
	type certificate struct {
		Domain      domain `json:"domain"`
		Certificate string `json:"certificate"`
		Key         string `json:"key"`
	}

	var certificates []certificate
	for main, sans := range entries {
		certPEM, keyPEM := makeCertPEMWithValidity(t, notBefore, notAfter, append([]string{main}, sans...)...)
		certificates = append(certificates, certificate{
			Domain:      domain{Main: main, SANs: sans},
			Certificate: base64.StdEncoding.EncodeToString(certPEM),
			Key:         base64.StdEncoding.EncodeToString(keyPEM),
		})
	}

	store := map[string]any{
		resolverName: map[string]any{
			// An Account block is always present and must be ignored.
			"Account":      map[string]any{"Email": "ops@example.test"},
			"certificates": certificates,
		},
	}

	raw, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("marshal store: %v", err)
	}

	path := filepath.Join(dir, "acme.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write store: %v", err)
	}
	return path
}

func TestAcmeCertWatcher_ServesCertificateBySNI(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStore(t, dir, "letsencrypt", map[string][]string{
		"mail.example.test":    nil,
		"webmail.example.test": nil,
	})

	watcher, err := NewAcmeCertWatcher(dir, "acme.json", "mail.example.test")
	if err != nil {
		t.Fatalf("NewAcmeCertWatcher: %v", err)
	}

	cert, err := watcher.GetCertificate(&tls.ClientHelloInfo{ServerName: "webmail.example.test"})
	if err != nil || cert == nil {
		t.Fatalf("GetCertificate: cert=%v err=%v", cert, err)
	}
}

// The resolver name varies per Coolify installation, so the parser must not
// depend on it (PLAN.md section 8.2, trap 3).
func TestAcmeCertWatcher_HandlesAnyResolverName(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStore(t, dir, "some-custom-resolver-name", map[string][]string{"mail.example.test": nil})

	watcher, err := NewAcmeCertWatcher(dir, "acme.json", "mail.example.test")
	if err != nil {
		t.Fatalf("NewAcmeCertWatcher: %v", err)
	}
	if !watcher.HasCertificateFor("mail.example.test") {
		t.Error("certificate was not found under a non-default resolver name")
	}
}

// A SAN entry must be served too, not just the main domain.
func TestAcmeCertWatcher_IndexesSubjectAlternativeNames(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStore(t, dir, "letsencrypt", map[string][]string{
		"example.test": {"mta-sts.example.test", "autoconfig.example.test"},
	})

	watcher, _ := NewAcmeCertWatcher(dir, "acme.json", "example.test")

	for _, name := range []string{"example.test", "mta-sts.example.test", "autoconfig.example.test"} {
		if !watcher.HasCertificateFor(name) {
			t.Errorf("no certificate indexed for %s", name)
		}
	}
}

// Traefik replaces the file by rename. A watcher bound to the old inode would
// never see this; polling the path does (PLAN.md section 8.2, trap 1).
func TestAcmeCertWatcher_PicksUpAtomicRename(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStore(t, dir, "letsencrypt", map[string][]string{"mail.example.test": nil})

	watcher, err := NewAcmeCertWatcher(dir, "acme.json", "mail.example.test")
	if err != nil {
		t.Fatalf("NewAcmeCertWatcher: %v", err)
	}
	if watcher.HasCertificateFor("new.example.test") {
		t.Fatal("the new host is present before the renewal")
	}

	// Simulate Traefik: write a temporary file, then rename it over the old one.
	staging := t.TempDir()
	writeAcmeStore(t, staging, "letsencrypt", map[string][]string{
		"mail.example.test": nil,
		"new.example.test":  nil,
	})
	if err := os.Rename(filepath.Join(staging, "acme.json"), filepath.Join(dir, "acme.json")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if err := watcher.reload(); err != nil {
		t.Fatalf("reload after rename: %v", err)
	}
	if !watcher.HasCertificateFor("new.example.test") {
		t.Error("the renewed store was not picked up after the atomic rename")
	}
}

// An unreadable or truncated file must never replace a working store with an
// empty one (PLAN.md section 8.4).
func TestAcmeCertWatcher_KeepsPreviousStoreOnParseFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeAcmeStore(t, dir, "letsencrypt", map[string][]string{"mail.example.test": nil})

	watcher, _ := NewAcmeCertWatcher(dir, "acme.json", "mail.example.test")

	if err := os.WriteFile(path, []byte("{ truncated"), 0o600); err != nil {
		t.Fatalf("write truncated file: %v", err)
	}
	if err := watcher.reload(); err == nil {
		t.Fatal("expected reload to report the parse failure")
	}

	if !watcher.HasCertificateFor("mail.example.test") {
		t.Error("a truncated file wiped out the certificates already loaded")
	}
}

// A store with no certificate for the mail host is a deployment error: no
// router was declared for it, so Traefik never requested one.
func TestAcmeCertWatcher_ReportsMissingMailHost(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStore(t, dir, "letsencrypt", map[string][]string{"webmail.example.test": nil})

	watcher, _ := NewAcmeCertWatcher(dir, "acme.json", "mail.example.test")

	if watcher.HasCertificateFor("mail.example.test") {
		t.Error("HasCertificateFor must report the missing mail host so the preflight can fail")
	}
}

// An unknown SNI falls back to the mail host certificate and is counted.
func TestAcmeCertWatcher_UnknownSniFallsBackAndCounts(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStore(t, dir, "letsencrypt", map[string][]string{"mail.example.test": nil})

	watcher, _ := NewAcmeCertWatcher(dir, "acme.json", "mail.example.test")

	cert, err := watcher.GetCertificate(&tls.ClientHelloInfo{ServerName: "stranger.invalid"})
	if err != nil || cert == nil {
		t.Fatalf("expected the default certificate, got cert=%v err=%v", cert, err)
	}
	if watcher.UnknownSNICount() != 1 {
		t.Errorf("UnknownSNICount = %d, want 1", watcher.UnknownSNICount())
	}
}

// A wildcard certificate must cover the hosts under it.
func TestAcmeCertWatcher_MatchesWildcard(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStore(t, dir, "letsencrypt", map[string][]string{"*.example.test": nil})

	watcher, _ := NewAcmeCertWatcher(dir, "acme.json", "*.example.test")

	if _, err := watcher.GetCertificate(&tls.ClientHelloInfo{ServerName: "mail.example.test"}); err != nil {
		t.Errorf("wildcard certificate did not cover mail.example.test: %v", err)
	}
}

// A missing file at startup is fatal on purpose: the operator must know
// immediately, not ninety days later.
func TestAcmeCertWatcher_FailsWhenStoreMissing(t *testing.T) {
	if _, err := NewAcmeCertWatcher(t.TempDir(), "acme.json", "mail.example.test"); err == nil {
		t.Fatal("expected construction to fail without an acme.json")
	}
}

// An identical file must not cause a pointless swap, but the watcher must
// still record that it looked. acme.json sits untouched for sixty days, so a
// LastReload that only advanced on change could not tell a healthy idle
// watcher apart from one that went blind (PLAN.md risk R2).
func TestAcmeCertWatcher_SkipsSwapButRecordsCheckWhenContentUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStore(t, dir, "letsencrypt", map[string][]string{"mail.example.test": nil})

	watcher, _ := NewAcmeCertWatcher(dir, "acme.json", "mail.example.test")
	firstReload := watcher.LastReload()
	firstChange := watcher.LastChange()

	time.Sleep(2 * time.Millisecond)
	if err := watcher.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if !watcher.LastChange().Equal(firstChange) {
		t.Error("an unchanged file swapped the certificate store")
	}
	if !watcher.LastReload().After(firstReload) {
		t.Error("LastReload did not advance, so a blind watcher would look identical to an idle one")
	}
}

// An expired entry must not be served: every peer would reject the handshake,
// whereas skipping it lets the SNI fallback take over (PLAN.md section 8.2).
func TestAcmeCertWatcher_SkipsExpiredCertificate(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStoreWithValidity(t, dir, "letsencrypt",
		map[string][]string{"expired.example.test": nil}, -48*time.Hour, -24*time.Hour)

	if _, err := NewAcmeCertWatcher(dir, "acme.json", "expired.example.test"); err == nil {
		t.Fatal("a store holding only an expired certificate must not load as usable")
	}
}

// The soonest expiry is what decides when the deployment breaks, so it has to
// be readable for the alerts of PLAN.md section 8.4.
func TestAcmeCertWatcher_ReportsEarliestExpiry(t *testing.T) {
	dir := t.TempDir()
	writeAcmeStoreWithValidity(t, dir, "letsencrypt",
		map[string][]string{"mail.example.test": nil}, -time.Hour, 72*time.Hour)

	watcher, err := NewAcmeCertWatcher(dir, "acme.json", "mail.example.test")
	if err != nil {
		t.Fatalf("NewAcmeCertWatcher: %v", err)
	}

	host, expiry, ok := watcher.EarliestExpiry()
	if !ok {
		t.Fatal("EarliestExpiry reported nothing for a loaded store")
	}
	if host != "mail.example.test" {
		t.Errorf("EarliestExpiry host = %q, want mail.example.test", host)
	}
	if remaining := time.Until(expiry); remaining > 73*time.Hour || remaining < 71*time.Hour {
		t.Errorf("expiry is %s away, want roughly 72h", remaining)
	}
}
