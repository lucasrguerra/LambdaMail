package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"lambdamail/protocols/internal/domain/entity"
)

func TestReportRepository_SaveDmarcAndTlsRpt(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set - skipping Postgres integration test")
	}

	ctx := context.Background()
	pool, err := NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	repo := NewReportRepository(pool)

	// Save DMARC
	dmarc := &entity.DmarcReport{
		OrgName:        "google.com",
		ReportID:       "r123",
		Domain:         "example.test",
		DateRangeBegin: time.Now().Add(-24 * time.Hour),
		DateRangeEnd:   time.Now(),
		Records: []entity.DmarcRecord{
			{SourceIP: "192.0.2.1", Count: 5, Disposition: "none", DKIMResult: "pass", SPFResult: "pass", HeaderFrom: "example.test"},
		},
	}

	if err := repo.SaveDmarcReport(ctx, dmarc); err != nil {
		t.Fatalf("SaveDmarcReport: %v", err)
	}

	// Save TLS-RPT
	tlsRpt := &entity.TlsRptReport{
		OrganizationName: "Google LLC",
		ReportID:         "tr123",
		Domain:           "example.test",
		DateRangeBegin:   time.Now().Add(-24 * time.Hour),
		DateRangeEnd:     time.Now(),
		Policies: []entity.TlsRptPolicy{
			{PolicyType: "sts", SuccessCount: 10, FailureCount: 0},
		},
	}

	if err := repo.SaveTlsRptReport(ctx, tlsRpt); err != nil {
		t.Fatalf("SaveTlsRptReport: %v", err)
	}
}
