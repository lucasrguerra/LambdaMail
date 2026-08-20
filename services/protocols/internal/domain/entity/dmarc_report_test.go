package entity

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestParseDmarcXmlReport_ValidXML(t *testing.T) {
	xmlData := `<?xml version="1.0"?>
<feedback>
  <report_metadata>
    <org_name>google.com</org_name>
    <report_id>123456</report_id>
    <date_range>
      <begin>1609459200</begin>
      <end>1609545600</end>
    </date_range>
  </report_metadata>
  <policy_published>
    <domain>example.test</domain>
  </policy_published>
  <record>
    <row>
      <source_ip>198.51.100.1</source_ip>
      <count>10</count>
      <policy_evaluated>
        <disposition>none</disposition>
        <dkim>pass</dkim>
        <spf>pass</spf>
      </policy_evaluated>
    </row>
    <identifiers>
      <header_from>example.test</header_from>
    </identifiers>
  </record>
</feedback>`

	report, err := ParseDmarcXmlReport([]byte(xmlData))
	if err != nil {
		t.Fatalf("ParseDmarcXmlReport: %v", err)
	}

	if report.OrgName != "google.com" || report.Domain != "example.test" {
		t.Errorf("unexpected report metadata: %+v", report)
	}
	if len(report.Records) != 1 || report.Records[0].Count != 10 {
		t.Errorf("unexpected records: %+v", report.Records)
	}

	// Test Gzip handling
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	gw.Write([]byte(xmlData))
	gw.Close()

	gzReport, err := ParseDmarcXmlReport(gzBuf.Bytes())
	if err != nil || gzReport.OrgName != "google.com" {
		t.Fatalf("ParseDmarcXmlReport gzip: %v", err)
	}
}

func TestParseTlsRptReport_ValidJSON(t *testing.T) {
	jsonData := `{
		"organization-name": "Google LLC",
		"report-id": "2026-07-25-001",
		"date-range": {
			"start-datetime": "2026-07-25T00:00:00Z",
			"end-datetime": "2026-07-25T23:59:59Z"
		},
		"policies": [
			{
				"policy": {
					"policy-type": "sts",
					"policy-domain": "example.test"
				},
				"summary": {
					"total-successful-session-count": 100,
					"total-failure-session-count": 0
				}
			}
		]
	}`

	report, err := ParseTlsRptReport([]byte(jsonData))
	if err != nil {
		t.Fatalf("ParseTlsRptReport: %v", err)
	}

	if report.OrganizationName != "Google LLC" || report.Domain != "example.test" {
		t.Errorf("unexpected tlsrpt metadata: %+v", report)
	}
	if len(report.Policies) != 1 || report.Policies[0].SuccessCount != 100 {
		t.Errorf("unexpected policies: %+v", report.Policies)
	}
}
