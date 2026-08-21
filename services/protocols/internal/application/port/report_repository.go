package port

import (
	"context"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/domain/entity"
)

// ReportRepository manages storage for ingested DMARC and TLS-RPT reports.
type ReportRepository interface {
	SaveDmarcReport(ctx context.Context, report *entity.DmarcReport) error
	SaveTlsRptReport(ctx context.Context, report *entity.TlsRptReport) error
}

// StoredReportMessage is one already-delivered message that looks like a
// report: it was addressed to a report alias and is still filed in a mailbox.
type StoredReportMessage struct {
	MessageID        uuid.UUID
	BlobID           uuid.UUID
	RecipientAddress string
}

// DeliveredReportStore reaches the reports that were delivered before
// automatic ingestion existed, so they can be parsed after the fact.
type DeliveredReportStore interface {
	// ListUningestedReports returns stored messages addressed to a report
	// alias. limit caps the batch; 0 means a full pass.
	ListUningestedReports(ctx context.Context, limit int) ([]StoredReportMessage, error)
	// MoveToReportsFolder files a backfilled message out of the inbox.
	MoveToReportsFolder(ctx context.Context, messageID uuid.UUID) error
}
