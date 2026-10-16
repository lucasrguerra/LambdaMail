package httppresentation

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"lambdamail/protocols/internal/domain/bimi"
)

// memoryBimiStore is the store without a database.
type memoryBimiStore struct {
	logos map[string]*BimiLogo
}

func (m *memoryBimiStore) Save(_ context.Context, domain string, svg []byte, vmcURL string) (string, error) {
	held := m.logos[domain]
	logo := &BimiLogo{SVG: svg, ETag: "etag-" + domain, VmcURL: vmcURL}
	if held != nil {
		// Replacing the logo keeps a held certificate, as the real store does
		logo.VmcPEM = held.VmcPEM
	}
	m.logos[domain] = logo
	return logo.ETag, nil
}

func (m *memoryBimiStore) Load(_ context.Context, domain string) (*BimiLogo, error) {
	return m.logos[domain], nil
}

func (m *memoryBimiStore) Delete(_ context.Context, domain string) error {
	delete(m.logos, domain)
	return nil
}

func (m *memoryBimiStore) SaveCertificate(_ context.Context, domain string, pem []byte) (bool, error) {
	logo := m.logos[domain]
	if logo == nil {
		return false, nil
	}
	logo.VmcPEM = pem
	return true, nil
}

func (m *memoryBimiStore) DeleteCertificate(_ context.Context, domain string) error {
	if logo := m.logos[domain]; logo != nil {
		logo.VmcPEM = nil
	}
	return nil
}

type fakeBimiDNS map[string][]string

func (f fakeBimiDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if records, ok := f[name]; ok {
		return records, nil
	}
	return nil, errors.New("no such host")
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../domain/bimi/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The certificates in the fixtures are valid on this date.
var bimiTestNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func newBimiRouter(store *memoryBimiStore) *Router {
	r := &Router{mux: http.NewServeMux()}
	r.bimi = &bimiAPI{
		store:         store,
		sessions:      testVerifier(interopSecret),
		defaultDomain: "ebay.com",
		mailHost:      "mail.ebay.com",
		now:           func() time.Time { return bimiTestNow },
		dns:           fakeBimiDNS{},
		fetch: func(context.Context, string) (bimi.Response, error) {
			return bimi.Response{}, errors.New("offline")
		},
	}
	r.mux.HandleFunc("/.well-known/bimi/default.svg", r.handleBimiLogo)
	r.mux.HandleFunc("/.well-known/bimi/vmc.pem", r.handleBimiCertificate)
	r.mux.HandleFunc("/api/v1/admin/bimi", r.handleBimiAdmin)
	r.mux.HandleFunc("/api/v1/admin/bimi/certificate", r.handleBimiCertificateAdmin)
	r.mux.HandleFunc("/api/v1/admin/bimi/check", r.handleBimiCheck)
	return r
}

func bimiCall(r *Router, method, target, body string, authed bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if authed {
		req.Header.Set("Authorization", "Bearer "+adminSurfaceToken2())
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

func jsonBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON (%d): %s", rec.Code, rec.Body)
	}
	return out
}

func putLogo(t *testing.T, r *Router, svg []byte) *httptest.ResponseRecorder {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"svg": string(svg)})
	return bimiCall(r, http.MethodPut, "/api/v1/admin/bimi?domain=ebay.com", string(payload), true)
}

func putCertificate(r *Router, pem []byte) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(map[string]string{"pem": string(pem)})
	return bimiCall(r, http.MethodPut, "/api/v1/admin/bimi/certificate?domain=ebay.com", string(payload), true)
}

func TestBimiAdminRoutesNeedAnAdminSession(t *testing.T) {
	r := newBimiRouter(&memoryBimiStore{logos: map[string]*BimiLogo{}})
	for _, target := range []string{
		"/api/v1/admin/bimi?domain=ebay.com",
		"/api/v1/admin/bimi/certificate?domain=ebay.com",
		"/api/v1/admin/bimi/check?domain=ebay.com",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			if rec := bimiCall(r, method, target, "{}", false); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a session answered %d", method, target, rec.Code)
			}
		}
	}
	if rec := bimiCall(r, http.MethodGet, "/api/v1/admin/bimi/check", "", true); rec.Code != http.StatusBadRequest {
		t.Errorf("no domain answered %d", rec.Code)
	}
}

func TestBimiCertificateNeedsALogoFirst(t *testing.T) {
	r := newBimiRouter(&memoryBimiStore{logos: map[string]*BimiLogo{}})
	rec := putCertificate(r, fixture(t, "ebay.pem"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body)
	}
}

func TestBimiAcceptsTheRealCertificateAndServesIt(t *testing.T) {
	store := &memoryBimiStore{logos: map[string]*BimiLogo{}}
	r := newBimiRouter(store)

	if rec := putLogo(t, r, fixture(t, "ebay.svg")); rec.Code != http.StatusOK {
		t.Fatalf("the real logo was refused: %s", rec.Body)
	}
	if rec := bimiCall(r, http.MethodGet, "/.well-known/bimi/vmc.pem", "", false); rec.Code != http.StatusNotFound {
		t.Errorf("before upload the public URL answered %d, want 404", rec.Code)
	}

	rec := putCertificate(r, fixture(t, "ebay.pem"))
	if rec.Code != http.StatusOK {
		t.Fatalf("the real certificate was refused: %s", rec.Body)
	}
	cert := jsonBody(t, rec)["certificate"].(map[string]any)
	if cert["valid"] != true || cert["matches_logo"] != true || cert["kind"] != "vmc" {
		t.Errorf("certificate report = %v", cert)
	}

	// The public document: no session, the PEM, cacheable, and conditional
	public := bimiCall(r, http.MethodGet, "/.well-known/bimi/vmc.pem", "", false)
	if public.Code != http.StatusOK || !strings.Contains(public.Body.String(), "BEGIN CERTIFICATE") {
		t.Fatalf("public answered %d", public.Code)
	}
	if got := public.Header().Get("Content-Type"); got != "application/x-pem-file" {
		t.Errorf("content type = %q", got)
	}
	if public.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(public.Header().Get("Cache-Control"), "public") {
		t.Errorf("headers = %v", public.Header())
	}
	conditional := httptest.NewRequest(http.MethodGet, "/.well-known/bimi/vmc.pem", nil)
	conditional.Header.Set("If-None-Match", public.Header().Get("ETag"))
	again := httptest.NewRecorder()
	r.mux.ServeHTTP(again, conditional)
	if again.Code != http.StatusNotModified {
		t.Errorf("conditional request answered %d", again.Code)
	}

	// The console reads it back, with the URLs the DNS record should carry
	got := jsonBody(t, bimiCall(r, http.MethodGet, "/api/v1/admin/bimi?domain=ebay.com", "", true))
	if got["logo_url"] != "https://mail.ebay.com/.well-known/bimi/default.svg" || got["certificate_url"] != "https://mail.ebay.com/.well-known/bimi/vmc.pem" {
		t.Errorf("urls = %v / %v", got["logo_url"], got["certificate_url"])
	}
	if got["certificate"].(map[string]any)["present"] != true {
		t.Errorf("certificate = %v", got["certificate"])
	}

	// Removing it leaves the logo
	if rec := bimiCall(r, http.MethodDelete, "/api/v1/admin/bimi/certificate?domain=ebay.com", "", true); rec.Code != http.StatusOK {
		t.Fatalf("delete answered %d", rec.Code)
	}
	if rec := bimiCall(r, http.MethodGet, "/.well-known/bimi/vmc.pem", "", false); rec.Code != http.StatusNotFound {
		t.Errorf("after delete the public URL answered %d", rec.Code)
	}
	if rec := bimiCall(r, http.MethodGet, "/.well-known/bimi/default.svg", "", false); rec.Code != http.StatusOK {
		t.Errorf("the logo disappeared with the certificate: %d", rec.Code)
	}
}

func TestBimiRefusesACertificateForAnotherLogo(t *testing.T) {
	store := &memoryBimiStore{logos: map[string]*BimiLogo{}}
	r := newBimiRouter(store)
	// CNN's logo is valid SVG Tiny PS but is not the one eBay's certificate holds
	if rec := putLogo(t, r, fixture(t, "cnn.svg")); rec.Code != http.StatusOK {
		t.Fatalf("logo refused: %s", rec.Body)
	}

	rec := putCertificate(r, fixture(t, "ebay.pem"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body)
	}
	violations, _ := jsonBody(t, rec)["violations"].([]any)
	if len(violations) == 0 || !strings.Contains(violations[0].(string), "logoMismatch") {
		t.Errorf("violations = %v", violations)
	}
	if len(store.logos["ebay.com"].VmcPEM) != 0 {
		t.Error("a certificate that does not match was stored")
	}
}

func TestBimiNeverStoresAPrivateKey(t *testing.T) {
	store := &memoryBimiStore{logos: map[string]*BimiLogo{}}
	r := newBimiRouter(store)
	putLogo(t, r, fixture(t, "ebay.svg"))

	key := "-----BEGIN PRIVATE KEY-----\nQUJD\n-----END PRIVATE KEY-----\n"
	rec := putCertificate(r, append(fixture(t, "ebay.pem"), key...))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("answered %d", rec.Code)
	}
	if len(store.logos["ebay.com"].VmcPEM) != 0 {
		t.Error("a private key was stored and would be served publicly")
	}
	if strings.Contains(rec.Body.String(), "QUJD") {
		t.Error("the refusal echoed the key")
	}
}

func TestBimiCertificateBodyMustBeJSON(t *testing.T) {
	r := newBimiRouter(&memoryBimiStore{logos: map[string]*BimiLogo{"ebay.com": {SVG: fixture(t, "ebay.svg")}}})
	if rec := bimiCall(r, http.MethodPut, "/api/v1/admin/bimi/certificate?domain=ebay.com", "-----BEGIN CERTIFICATE-----", true); rec.Code != http.StatusBadRequest {
		t.Errorf("answered %d", rec.Code)
	}
}

func TestBimiReplacingTheLogoWarnsAboutTheHeldCertificate(t *testing.T) {
	store := &memoryBimiStore{logos: map[string]*BimiLogo{}}
	r := newBimiRouter(store)
	putLogo(t, r, fixture(t, "ebay.svg"))
	putCertificate(r, fixture(t, "ebay.pem"))

	// Another valid logo: the certificate no longer vouches for what is served
	rec := putLogo(t, r, fixture(t, "cnn.svg"))
	if rec.Code != http.StatusOK || jsonBody(t, rec)["certificate_mismatch"] != true {
		t.Fatalf("no warning: %d %s", rec.Code, rec.Body)
	}
	cert := jsonBody(t, bimiCall(r, http.MethodGet, "/api/v1/admin/bimi?domain=ebay.com", "", true))["certificate"].(map[string]any)
	if cert["valid"] != false || cert["matches_logo"] != false {
		t.Errorf("the console should show the certificate as no longer matching: %v", cert)
	}
}

func TestBimiCheckReportsEachStep(t *testing.T) {
	store := &memoryBimiStore{logos: map[string]*BimiLogo{}}
	r := newBimiRouter(store)
	putLogo(t, r, fixture(t, "ebay.svg"))

	rec := bimiCall(r, http.MethodGet, "/api/v1/admin/bimi/check?domain=ebay.com", "", true)
	body := jsonBody(t, rec)
	if rec.Code != http.StatusOK || body["ready"] != false {
		t.Fatalf("with nothing published it must not be ready: %d %v", rec.Code, body)
	}
	ids := map[string]string{}
	for _, c := range body["checks"].([]any) {
		m := c.(map[string]any)
		ids[m["id"].(string)] = m["status"].(string)
	}
	if ids["logo"] != "ok" || ids["dmarc"] != "fail" || ids["record"] != "fail" {
		t.Errorf("checks = %v", ids)
	}

	none := bimiCall(r, http.MethodGet, "/api/v1/admin/bimi/check?domain=unknown.test", "", true)
	if jsonBody(t, none)["configured"] != false {
		t.Errorf("a domain with no logo: %s", none.Body)
	}
}

func TestPublicAddress(t *testing.T) {
	for address, want := range map[string]bool{
		"8.8.8.8":         true,
		"1.1.1.1":         true,
		"2001:4860::8888": true,
		"127.0.0.1":       false,
		"10.0.0.5":        false,
		"172.16.3.4":      false,
		"192.168.1.1":     false,
		"169.254.169.254": false,
		"100.64.0.1":      false,
		"0.0.0.0":         false,
		"::1":             false,
		"fe80::1":         false,
		"fc00::1":         false,
		"224.0.0.1":       false,
	} {
		if got := publicAddress(net.ParseIP(address)); got != want {
			t.Errorf("publicAddress(%s) = %v, want %v", address, got, want)
		}
	}
}

// The fetcher is what keeps the console's checks from reaching this server's
// own network. It refuses a literal private address before any request leaves.
func TestFetcherRefusesPrivateAndPlainHTTP(t *testing.T) {
	fetch := newBimiFetcher()
	for _, url := range []string{"http://example.test/logo.svg", "https://127.0.0.1/logo.svg", "https://169.254.169.254/latest/meta-data/", "https://[::1]/x"} {
		if _, err := fetch(context.Background(), url); err == nil {
			t.Errorf("%s was fetched", url)
		}
	}
}

// publishRecorder collects the domains the console asked to have published.
type publishRecorder struct{ domains chan string }

func newPublishRecorder() *publishRecorder { return &publishRecorder{domains: make(chan string, 8)} }

func (p *publishRecorder) record(domain string) { p.domains <- domain }

func (p *publishRecorder) expect(t *testing.T, what string) {
	t.Helper()
	select {
	case got := <-p.domains:
		if got != "ebay.com" {
			t.Errorf("%s published %q", what, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not ask for the DNS record to be published", what)
	}
}

func (p *publishRecorder) expectNothing(t *testing.T, what string) {
	t.Helper()
	select {
	case got := <-p.domains:
		t.Fatalf("%s published %q though it changed nothing", what, got)
	case <-time.After(150 * time.Millisecond):
	}
}

// The record in the zone follows the picture: every change to the logo or the
// certificate asks for the DNS to be reconciled, and nothing else does.
func TestBimiChangesAreHandedToThePublisher(t *testing.T) {
	store := &memoryBimiStore{logos: map[string]*BimiLogo{}}
	r := newBimiRouter(store)
	rec := newPublishRecorder()
	r.bimi.publish = rec.record

	putLogo(t, r, fixture(t, "ebay.svg"))
	rec.expect(t, "saving the logo")

	putCertificate(r, fixture(t, "ebay.pem"))
	rec.expect(t, "saving the certificate")

	// Reads and refused uploads change nothing and publish nothing
	bimiCall(r, http.MethodGet, "/api/v1/admin/bimi?domain=ebay.com", "", true)
	putCertificate(r, []byte("-----BEGIN PRIVATE KEY-----\nQUJD\n-----END PRIVATE KEY-----\n"))
	putLogo(t, r, []byte("<svg/>"))
	rec.expectNothing(t, "a read or a refused upload")

	bimiCall(r, http.MethodDelete, "/api/v1/admin/bimi/certificate?domain=ebay.com", "", true)
	rec.expect(t, "removing the certificate")

	bimiCall(r, http.MethodDelete, "/api/v1/admin/bimi?domain=ebay.com", "", true)
	rec.expect(t, "removing the logo")
}

func TestBimiWithoutAPublisherStillWorks(t *testing.T) {
	r := newBimiRouter(&memoryBimiStore{logos: map[string]*BimiLogo{}})
	resp := jsonBody(t, putLogo(t, r, fixture(t, "ebay.svg")))
	if resp["ok"] != true || resp["dns_publishing"] != false {
		t.Fatalf("%v", resp)
	}
}

// The check expects what the reconciler writes: with a certificate that no
// longer matches the logo that is a record without a=, and that is correct.
func TestBimiCheckExpectsWhatTheReconcilerPublishes(t *testing.T) {
	store := &memoryBimiStore{logos: map[string]*BimiLogo{}}
	r := newBimiRouter(store)
	putLogo(t, r, fixture(t, "cnn.svg"))
	store.logos["ebay.com"].VmcPEM = fixture(t, "ebay.pem") // a certificate for another picture

	r.bimi.dns = fakeBimiDNS{
		"_dmarc.ebay.com":        {"v=DMARC1; p=reject"},
		"default._bimi.ebay.com": {"v=BIMI1; l=https://mail.ebay.com/.well-known/bimi/default.svg;"},
	}
	body := jsonBody(t, bimiCall(r, http.MethodGet, "/api/v1/admin/bimi/check?domain=ebay.com", "", true))

	status := map[string]string{}
	detail := map[string]string{}
	for _, c := range body["checks"].([]any) {
		m := c.(map[string]any)
		status[m["id"].(string)] = m["status"].(string)
		detail[m["id"].(string)] = m["detail"].(string)
	}
	if status["record"] != "ok" {
		t.Errorf("the record without a= is what the reconciler writes here, but the check said %s: %s", status["record"], detail["record"])
	}
	if status["certificate"] != "fail" || !strings.Contains(detail["certificate"], "NOT published") {
		t.Errorf("the certificate check must say it is held back: %s %s", status["certificate"], detail["certificate"])
	}
}

// The console shows the record the reconciler writes, taken from the server
// rather than put together again in the browser.
func TestBimiGetReportsTheRecordItWouldPublish(t *testing.T) {
	store := &memoryBimiStore{logos: map[string]*BimiLogo{}}
	r := newBimiRouter(store)
	putLogo(t, r, fixture(t, "ebay.svg"))
	read := func() map[string]any {
		return jsonBody(t, bimiCall(r, http.MethodGet, "/api/v1/admin/bimi?domain=ebay.com", "", true))
	}

	got := read()
	if got["dns_record"] != "v=BIMI1; l=https://mail.ebay.com/.well-known/bimi/default.svg;" || got["certificate_withheld"] != "" {
		t.Fatalf("logo only: %v", got)
	}

	putCertificate(r, fixture(t, "ebay.pem"))
	if got := read(); got["dns_record"] != "v=BIMI1; l=https://mail.ebay.com/.well-known/bimi/default.svg; a=https://mail.ebay.com/.well-known/bimi/vmc.pem;" {
		t.Fatalf("with its certificate: %v", got["dns_record"])
	}

	// The logo is replaced: the certificate is held but no longer advertised
	putLogo(t, r, fixture(t, "cnn.svg"))
	got = read()
	if got["certificate_withheld"] != "logoMismatch" || strings.Contains(got["dns_record"].(string), "a=") {
		t.Fatalf("after replacing the logo: %v / %v", got["dns_record"], got["certificate_withheld"])
	}
}
