package usecase

import (
	"context"
	"testing"

	"lambdamail/protocols/internal/domain/entity"
)

type fakeReportRepository struct {
	dmarcReports  []*entity.DmarcReport
	tlsRptReports []*entity.TlsRptReport
}

func (f *fakeReportRepository) SaveDmarcReport(_ context.Context, report *entity.DmarcReport) error {
	f.dmarcReports = append(f.dmarcReports, report)
	return nil
}

func (f *fakeReportRepository) SaveTlsRptReport(_ context.Context, report *entity.TlsRptReport) error {
	f.tlsRptReports = append(f.tlsRptReports, report)
	return nil
}

func TestIngestReportsUseCase_IngestDmarcAndTlsRpt(t *testing.T) {
	repo := &fakeReportRepository{}
	uc := NewIngestReportsUseCase(repo)

	ctx := context.Background()

	// DMARC XML
	dmarcPayload := `<?xml version="1.0"?>
<feedback>
  <report_metadata><org_name>test.org</org_name><report_id>r1</report_id><date_range><begin>100</begin><end>200</end></date_range></report_metadata>
  <policy_published><domain>test.domain</domain></policy_published>
  <record><row><source_ip>1.2.3.4</source_ip><count>5</count><policy_evaluated><disposition>none</disposition><dkim>pass</dkim><spf>pass</spf></policy_evaluated></row><identifiers><header_from>test.domain</header_from></identifiers></record>
</feedback>`

	report1, err := uc.IngestDmarc(ctx, []byte(dmarcPayload))
	if err != nil || report1.OrgName != "test.org" {
		t.Fatalf("IngestDmarc: %v", err)
	}
	if len(repo.dmarcReports) != 1 {
		t.Errorf("dmarcReports count = %d, want 1", len(repo.dmarcReports))
	}

	// TLS-RPT JSON
	tlsRptPayload := `{
		"organization-name": "test.org",
		"report-id": "r2",
		"date-range": {"start-datetime": "2026-07-25T00:00:00Z", "end-datetime": "2026-07-25T23:59:59Z"},
		"policies": [{"policy": {"policy-type": "sts", "domain": "test.domain"}, "summary": {"total-successful-session-count": 10, "total-failure-session-count": 0}}]
	}`

	report2, err := uc.IngestTlsRpt(ctx, []byte(tlsRptPayload))
	if err != nil || report2.OrganizationName != "test.org" {
		t.Fatalf("IngestTlsRpt: %v", err)
	}
	if len(repo.tlsRptReports) != 1 {
		t.Errorf("tlsRptReports count = %d, want 1", len(repo.tlsRptReports))
	}
}
