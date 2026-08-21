package main

import (
	"context"
	"log"
	"time"

	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
)

// runBackfillReports parses the DMARC and TLS-RPT reports that were delivered
// before automatic ingestion existed.
//
// Run by hand, once, after deploying ingestion: every report received until
// then was filed in the admin's inbox as an attachment nothing ever read, so
// the tables the console shows start empty however long the server has been
// running. Safe to run more than once - storing a report is idempotent, and a
// message already filed in Reports is not picked up again.
func runBackfillReports(cfg config) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("backfill: cannot reach the database: %v", err)
	}
	defer pool.Close()

	// The reader alone: the backfill only ever reads stored messages back out.
	blobs := diskstorage.NewLocalDiskBlobReader(pool)

	store := postgres.NewDeliveredReportStore(pool)
	uc := usecase.NewBackfillReportsUseCase(store, blobs,
		usecase.NewIngestDeliveredReportsUseCase(
			usecase.NewIngestReportsUseCase(postgres.NewReportRepository(pool))))

	summary, err := uc.Run(ctx, 0)
	if err != nil {
		log.Fatalf("backfill: %v", err)
	}

	log.Printf("backfill: scanned %d stored reports, ingested %d, could not read %d",
		summary.Scanned, summary.Ingested, summary.Failed)
	if summary.Failed > 0 {
		log.Printf("backfill: the unreadable ones are filed in Reports with their " +
			"bytes intact, so this can be run again once the cause is fixed")
	}
}
