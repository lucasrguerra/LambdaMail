package usecase

import (
	"context"
	"fmt"
	"log"

	"lambdamail/protocols/internal/application/port"
)

// BackfillReportsUseCase parses reports that were delivered before automatic
// ingestion existed.
//
// Every report received until now was filed in the admin's inbox as an
// attachment and never read, so the tables the admin console shows start
// empty however long the server has been running. The messages are still
// stored, so the backlog can be parsed after the fact and filed out of the
// inbox at the same time.
type BackfillReportsUseCase struct {
	store    port.DeliveredReportStore
	blobs    port.BlobReader
	ingestor *IngestDeliveredReportsUseCase
}

func NewBackfillReportsUseCase(
	store port.DeliveredReportStore,
	blobs port.BlobReader,
	ingestor *IngestDeliveredReportsUseCase,
) *BackfillReportsUseCase {
	return &BackfillReportsUseCase{store: store, blobs: blobs, ingestor: ingestor}
}

// BackfillSummary is what one run did, for the operator running it by hand.
type BackfillSummary struct {
	// Scanned is how many stored report messages were examined.
	Scanned int
	// Ingested is how many produced a report row.
	Ingested int
	// Failed is how many could not be read or parsed. They are left filed in
	// Reports with their bytes intact, so a later run can retry them.
	Failed int
}

// Run parses the backlog. limit caps how many messages are examined; 0 means
// whatever the store considers a full pass.
//
// Only a failure to read the backlog itself stops the run. An individual
// message that cannot be read or parsed is counted and skipped: one corrupt
// attachment from years ago must not block every report behind it.
func (uc *BackfillReportsUseCase) Run(ctx context.Context, limit int) (BackfillSummary, error) {
	summary := BackfillSummary{}

	pending, err := uc.store.ListUningestedReports(ctx, limit)
	if err != nil {
		return summary, fmt.Errorf("list the report backlog: %w", err)
	}

	for _, msg := range pending {
		summary.Scanned++

		raw, err := uc.blobs.ReadByID(ctx, msg.BlobID)
		if err != nil {
			summary.Failed++
			log.Printf("backfill: could not read the message for %s: %v", msg.RecipientAddress, err)
			continue
		}

		outcome, err := uc.ingestor.Ingest(ctx, []string{msg.RecipientAddress}, raw)
		switch {
		case err != nil:
			summary.Failed++
			log.Printf("backfill: could not ingest the message for %s: %v", msg.RecipientAddress, err)
		case outcome.Stored:
			summary.Ingested++
		default:
			// Recognised as a report but nothing parseable inside it.
			summary.Failed++
		}

		// Filed either way. The inbox is being cleared, and a report this
		// server cannot parse today is still a report - its bytes stay on
		// disk, so a later run can try again once the parser is fixed.
		if err := uc.store.MoveToReportsFolder(ctx, msg.MessageID); err != nil {
			log.Printf("backfill: could not file %s into %s: %v", msg.MessageID, ReportsFolderName, err)
		}
	}

	return summary, nil
}
