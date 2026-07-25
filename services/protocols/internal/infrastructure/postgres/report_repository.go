package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/domain/entity"
)

type ReportRepository struct {
	pool *pgxpool.Pool
}

func NewReportRepository(pool *pgxpool.Pool) *ReportRepository {
	return &ReportRepository{pool: pool}
}

func (r *ReportRepository) SaveDmarcReport(ctx context.Context, report *entity.DmarcReport) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var reportID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO dmarc_reports (org_name, report_id, domain, date_range_begin, date_range_end)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, report.OrgName, report.ReportID, report.Domain, report.DateRangeBegin, report.DateRangeEnd).Scan(&reportID)
	if err != nil {
		return fmt.Errorf("insert dmarc report header: %w", err)
	}

	for _, rec := range report.Records {
		_, err := tx.Exec(ctx, `
			INSERT INTO dmarc_report_records (report_id, source_ip, count, disposition, dkim_result, spf_result, header_from)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, reportID, rec.SourceIP, rec.Count, rec.Disposition, rec.DKIMResult, rec.SPFResult, rec.HeaderFrom)
		if err != nil {
			return fmt.Errorf("insert dmarc record: %w", err)
		}
	}

	return tx.Commit(ctx)
}

func (r *ReportRepository) SaveTlsRptReport(ctx context.Context, report *entity.TlsRptReport) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var reportID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO tls_rpt_reports (organization_name, report_id, domain, date_range_begin, date_range_end)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, report.OrganizationName, report.ReportID, report.Domain, report.DateRangeBegin, report.DateRangeEnd).Scan(&reportID)
	if err != nil {
		return fmt.Errorf("insert tls-rpt report header: %w", err)
	}

	for _, pol := range report.Policies {
		_, err := tx.Exec(ctx, `
			INSERT INTO tls_rpt_report_policies (report_id, policy_type, success_count, failure_count)
			VALUES ($1, $2, $3, $4)
		`, reportID, pol.PolicyType, pol.SuccessCount, pol.FailureCount)
		if err != nil {
			return fmt.Errorf("insert tls-rpt policy: %w", err)
		}
	}

	return tx.Commit(ctx)
}
