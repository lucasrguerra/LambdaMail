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

	// Idempotent on (org_name, report_id): the reporter's own identifier for
	// the report. Reports are now parsed out of inbound mail automatically, so
	// a redelivery - an SMTP retry, or a reporter simply sending again - is
	// routine, and storing the same report twice would double every number the
	// admin console shows.
	//
	// DO UPDATE rather than DO NOTHING so the existing row's id comes back and
	// the records below can be reconciled against it; DO NOTHING returns no
	// row at all when it conflicts.
	var reportID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO dmarc_reports (org_name, report_id, domain, date_range_begin, date_range_end)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_name, report_id)
		DO UPDATE SET domain = EXCLUDED.domain
		RETURNING id
	`, report.OrgName, report.ReportID, report.Domain, report.DateRangeBegin, report.DateRangeEnd).Scan(&reportID)
	if err != nil {
		return fmt.Errorf("insert dmarc report header: %w", err)
	}

	// The records are replaced, not appended: on a redelivery the report's
	// previous rows are still here, and adding another set would double every
	// count derived from them.
	if _, err := tx.Exec(ctx,
		`DELETE FROM dmarc_report_records WHERE report_id = $1`, reportID); err != nil {
		return fmt.Errorf("clear dmarc records: %w", err)
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

	// Idempotent for the same reason as the DMARC writer above.
	var reportID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO tls_rpt_reports (organization_name, report_id, domain, date_range_begin, date_range_end)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_name, report_id)
		DO UPDATE SET domain = EXCLUDED.domain
		RETURNING id
	`, report.OrganizationName, report.ReportID, report.Domain, report.DateRangeBegin, report.DateRangeEnd).Scan(&reportID)
	if err != nil {
		return fmt.Errorf("insert tls-rpt report header: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM tls_rpt_report_policies WHERE report_id = $1`, reportID); err != nil {
		return fmt.Errorf("clear tls-rpt policies: %w", err)
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
