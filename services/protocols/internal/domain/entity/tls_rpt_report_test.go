package entity

import (
	"bytes"
	"compress/gzip"
	"testing"
)

// This is a real report, as Google sends it daily: the gzipped JSON that
// arrives attached to a message addressed to the TLS-RPT rua.
const googleTlsRptReport = `{"organization-name":"Google Inc.",` +
	`"date-range":{"start-datetime":"2026-08-19T00:00:00Z","end-datetime":"2026-08-19T23:59:59Z"},` +
	`"contact-info":"smtp-tls-reporting@google.com","report-id":"2026-08-19T00:00:00Z_example.test",` +
	`"policies":[{"policy":{"policy-type":"no-policy-found","policy-domain":"example.test"},` +
	`"summary":{"total-successful-session-count":2,"total-failure-session-count":0}}]}`

// The field is "policy-domain" (RFC 8460 section 4.4), not "domain". Reading
// the wrong name parsed every report to a blank domain, so a stored report
// could not be attributed to the domain it was about.
func TestTlsRptReportCarriesThePolicyDomain(t *testing.T) {
	report, err := ParseTlsRptReport([]byte(googleTlsRptReport))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if report.Domain != "example.test" {
		t.Errorf("domain is %q, want example.test", report.Domain)
	}
	if report.OrganizationName != "Google Inc." {
		t.Errorf("organization is %q", report.OrganizationName)
	}
	if len(report.Policies) != 1 {
		t.Fatalf("want 1 policy, got %d", len(report.Policies))
	}
	if report.Policies[0].SuccessCount != 2 {
		t.Errorf("success count is %d, want 2", report.Policies[0].SuccessCount)
	}
	if report.Policies[0].PolicyType != "no-policy-found" {
		t.Errorf("policy type is %q", report.Policies[0].PolicyType)
	}
}

// Reports arrive gzipped, which is how they land as a .json.gz attachment.
func TestTlsRptReportParsesGzippedPayload(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(googleTlsRptReport)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := ParseTlsRptReport(buf.Bytes())
	if err != nil {
		t.Fatalf("parse gzipped: %v", err)
	}
	if report.Domain != "example.test" {
		t.Errorf("domain is %q, want example.test", report.Domain)
	}
}
