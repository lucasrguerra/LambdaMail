package httppresentation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Under TLS_MODE=acme the certificate is issued by this service, not watched
// on disk, so there is no watcher to be healthy or unhealthy. Reporting false
// there put a standing "unavailable" warning on a panel whose certificate was
// perfectly fine.
func TestTlsStatus_NoWatcherVerdictWithoutAReloadTime(t *testing.T) {
	api := &adminTlsAPI{
		source:     stillSource{},
		mailHost:   "mail.example.test",
		tlsMode:    "acme",
		sessions:   testVerifier(interopSecret),
		pollPeriod: time.Minute,
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tls", nil)
	req.Header.Set("Authorization", "Bearer "+adminSurfaceToken2())
	api.sessions.now = func() time.Time { return time.Unix(interopIssuedAt+60, 0) }
	api.handleStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if v, present := body["watcher_healthy"]; !present || v != nil {
		t.Errorf("watcher_healthy = %v, want null: there is no watcher to judge", v)
	}
}

// stillSource holds a certificate but never reloads: exactly what a
// self-issuing provider looks like.
type stillSource struct{}

func (stillSource) EarliestExpiry() (string, time.Time, bool) {
	return "mail.example.test", time.Now().Add(60 * 24 * time.Hour), true
}
func (stillSource) LastReload() time.Time         { return time.Time{} }
func (stillSource) LastChange() time.Time         { return time.Time{} }
func (stillSource) UnknownSNICount() int64        { return 0 }
func (stillSource) HasCertificateFor(string) bool { return true }
