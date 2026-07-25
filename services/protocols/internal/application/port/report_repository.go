package port

import (
	"context"

	"lambdamail/protocols/internal/domain/entity"
)

// ReportRepository manages storage for ingested DMARC and TLS-RPT reports.
type ReportRepository interface {
	SaveDmarcReport(ctx context.Context, report *entity.DmarcReport) error
	SaveTlsRptReport(ctx context.Context, report *entity.TlsRptReport) error
}
