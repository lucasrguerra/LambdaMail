package httppresentation

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appusecase "lambdamail/protocols/internal/application/usecase"
)

func TestRouter_MtaSts_Autoconfig_SecurityTxt_Ingest(t *testing.T) {
	uc := appusecase.NewIngestReportsUseCase(nil)
	router := NewRouter(uc, func() error { return nil })

	// 1. Health
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("health status = %d", w.Code)
	}

	// 2. MTA-STS
	req = httptest.NewRequest(http.MethodGet, "/.well-known/mta-sts.txt", nil)
	req.Host = "mta-sts.example.test"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "version: STSv1") {
		t.Errorf("mta-sts response = %q", w.Body.String())
	}

	// 3. Thunderbird Autoconfig
	req = httptest.NewRequest(http.MethodGet, "/mail/config-v1.1.xml", nil)
	req.Host = "autoconfig.example.test"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<clientConfig") {
		t.Errorf("autoconfig response = %q", w.Body.String())
	}

	// 4. Outlook Autodiscover
	req = httptest.NewRequest(http.MethodPost, "/autodiscover/autodiscover.xml", nil)
	req.Host = "autodiscover.example.test"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<Autodiscover") {
		t.Errorf("autodiscover response = %q", w.Body.String())
	}

	// 5. Security.txt
	req = httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil)
	req.Host = "example.test"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Contact: mailto:security@example.test") {
		t.Errorf("security.txt response = %q", w.Body.String())
	}

	// 6. Report Ingestion (DMARC)
	dmarcPayload := `<?xml version="1.0"?><feedback><report_metadata><org_name>org</org_name><report_id>id</report_id><date_range><begin>1</begin><end>2</end></date_range></report_metadata><policy_published><domain>dom</domain></policy_published></feedback>`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/reports/dmarc", bytes.NewBufferString(dmarcPayload))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("dmarc ingest status = %d, body = %q", w.Code, w.Body.String())
	}
}

// A certificate problem must be visible without taking the container down:
// restarting cannot fix a certificate Traefik never issued, so /health stays
// 200 and carries the degraded state instead (PLAN.md section 8.4).
func TestRouter_HealthReportsDegradedWithoutFailing(t *testing.T) {
	router := NewRouter(appusecase.NewIngestReportsUseCase(nil), func() error { return nil })
	router.SetDegradedCheck(func() error { return errors.New("self-signed certificate in use") })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("degraded health status = %d, want 200 so the orchestrator does not restart-loop", w.Code)
	}
	if got := w.Header().Get("X-LambdaMail-Health"); got != "degraded" {
		t.Errorf("X-LambdaMail-Health = %q, want degraded", got)
	}
	if !strings.Contains(w.Body.String(), "self-signed certificate in use") {
		t.Errorf("the degraded body does not name the cause: %q", w.Body.String())
	}
}

// A dependency that is actually down is a different state: that one must fail.
func TestRouter_HealthFailsWhenDependencyIsDown(t *testing.T) {
	router := NewRouter(appusecase.NewIngestReportsUseCase(nil), func() error { return errors.New("database unreachable") })

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("health status = %d, want 503", w.Code)
	}
}
