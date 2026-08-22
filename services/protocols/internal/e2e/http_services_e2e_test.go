package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/postgres"
	httppresentation "lambdamail/protocols/internal/presentation/http"
)

func TestHttpServicesEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(15437).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:15437/postgres?sslmode=disable"
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	applyMigrations(t, ctx, pool)

	reportRepo := postgres.NewReportRepository(pool)
	reportUseCase := appusecase.NewIngestReportsUseCase(reportRepo)
	router := httppresentation.NewRouter(reportUseCase, func() error { return pool.Ping(ctx) })

	ts := httptest.NewServer(router)
	defer ts.Close()

	client := ts.Client()

	// 1. MTA-STS policy check
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/.well-known/mta-sts.txt", nil)
	req.Host = "mta-sts.httpdomain.test"
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("mta-sts request failed: err=%v, code=%d", err, resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(bodyBytes), "version: STSv1") || !strings.Contains(string(bodyBytes), "mx: mail.httpdomain.test") {
		t.Errorf("mta-sts policy mismatch: %s", string(bodyBytes))
	}

	// 2. Thunderbird autoconfig XML check
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/mail/config-v1.1.xml", nil)
	req.Host = "autoconfig.httpdomain.test"
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("autoconfig request failed: err=%v, code=%d", err, resp.StatusCode)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(bodyBytes), "<clientConfig version=\"1.1\">") {
		t.Errorf("autoconfig xml mismatch: %s", string(bodyBytes))
	}

	// 3. Security.txt check
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/.well-known/security.txt", nil)
	req.Host = "httpdomain.test"
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("security.txt request failed: err=%v, code=%d", err, resp.StatusCode)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(bodyBytes), "Contact: mailto:security@httpdomain.test") {
		t.Errorf("security.txt mismatch: %s", string(bodyBytes))
	}

	// 4. Ingest DMARC report over HTTP and verify PostgreSQL persistence
	dmarcPayload := `<?xml version="1.0"?>
<feedback>
  <report_metadata><org_name>httporg.com</org_name><report_id>e2e123</report_id><date_range><begin>1600000000</begin><end>1600086400</end></date_range></report_metadata>
  <policy_published><domain>httpdomain.test</domain></policy_published>
  <record><row><source_ip>198.51.100.5</source_ip><count>12</count><policy_evaluated><disposition>none</disposition><dkim>pass</dkim><spf>pass</spf></policy_evaluated></row><identifiers><header_from>httpdomain.test</header_from></identifiers></record>
</feedback>`

	resp, err = client.Post(ts.URL+"/api/v1/reports/dmarc", "application/xml", bytes.NewBufferString(dmarcPayload))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("dmarc ingest failed: err=%v, code=%d", err, resp.StatusCode)
	}

	var dmarcCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM dmarc_reports WHERE org_name = 'httporg.com'`).Scan(&dmarcCount); err != nil || dmarcCount != 1 {
		t.Fatalf("expected 1 dmarc report in DB, got count=%d, err=%v", dmarcCount, err)
	}

	// 5. Ingest TLS-RPT report over HTTP and verify PostgreSQL persistence
	tlsRptPayload := `{
		"organization-name": "httporg.com",
		"report-id": "tls123",
		"date-range": {"start-datetime": "2026-07-25T00:00:00Z", "end-datetime": "2026-07-25T23:59:59Z"},
		"policies": [{"policy": {"policy-type": "sts", "domain": "httpdomain.test"}, "summary": {"total-successful-session-count": 50, "total-failure-session-count": 0}}]
	}`

	resp, err = client.Post(ts.URL+"/api/v1/reports/tlsrpt", "application/json", bytes.NewBufferString(tlsRptPayload))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("tls-rpt ingest failed: err=%v, code=%d", err, resp.StatusCode)
	}

	var tlsRptCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tls_rpt_reports WHERE organization_name = 'httporg.com'`).Scan(&tlsRptCount); err != nil || tlsRptCount != 1 {
		t.Fatalf("expected 1 tls-rpt report in DB, got count=%d, err=%v", tlsRptCount, err)
	}
}
