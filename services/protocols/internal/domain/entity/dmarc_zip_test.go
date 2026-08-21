package entity

import (
	"archive/zip"
	"bytes"
	"testing"
)

// A real DMARC aggregate report, trimmed to the fields the parser reads.
const dmarcAggregateXML = `<?xml version="1.0" encoding="UTF-8" ?>
<feedback>
  <report_metadata>
    <org_name>Yahoo</org_name>
    <report_id>1234567890</report_id>
    <date_range><begin>1787097600</begin><end>1787183999</end></date_range>
  </report_metadata>
  <policy_published>
    <domain>example.test</domain>
    <p>quarantine</p>
  </policy_published>
  <record>
    <row>
      <source_ip>203.0.113.10</source_ip>
      <count>3</count>
      <policy_evaluated><disposition>none</disposition><dkim>pass</dkim><spf>pass</spf></policy_evaluated>
    </row>
    <identifiers><header_from>example.test</header_from></identifiers>
  </record>
</feedback>`

// Microsoft and Yahoo send aggregate reports as .zip, not .gz. The parser
// handled gzip only, so every report from those two failed to parse - and
// between them they are a large share of the reports any domain receives.
func TestDmarcReportParsesZippedPayload(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("example.test!1787097600!1787183999.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(dmarcAggregateXML)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := ParseDmarcXmlReport(buf.Bytes())
	if err != nil {
		t.Fatalf("parse zipped dmarc report: %v", err)
	}
	if report.OrgName != "Yahoo" {
		t.Errorf("org name is %q, want Yahoo", report.OrgName)
	}
	if report.Domain != "example.test" {
		t.Errorf("domain is %q, want example.test", report.Domain)
	}
	if len(report.Records) != 1 {
		t.Fatalf("want 1 record, got %d", len(report.Records))
	}
}

// Plain XML must keep working exactly as before.
func TestDmarcReportStillParsesPlainXml(t *testing.T) {
	report, err := ParseDmarcXmlReport([]byte(dmarcAggregateXML))
	if err != nil {
		t.Fatalf("parse plain xml: %v", err)
	}
	if report.OrgName != "Yahoo" {
		t.Errorf("org name is %q", report.OrgName)
	}
}

// An empty archive has no report in it and must be reported as such rather
// than panicking on a missing first file.
func TestDmarcReportRejectsEmptyZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDmarcXmlReport(buf.Bytes()); err == nil {
		t.Error("an empty zip should not parse as a report")
	}
}
