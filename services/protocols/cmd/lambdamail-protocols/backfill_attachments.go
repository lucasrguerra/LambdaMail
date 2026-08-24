package main

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
)

// attachmentBackfillName identifies this repair in the backfills table.
const attachmentBackfillName = "has_attachments_single_part"

// backfillAttachmentFlags re-reads stored messages and marks the ones whose
// body is itself an attachment.
//
// has_attachments was only set for multipart messages, so a DMARC aggregate
// report - a single-part application/zip with no text at all - was filed as
// having none. Correcting the rule fixes new deliveries; the messages already
// in the mailbox need re-reading, which is what this does, once.
//
// It runs in the background and never blocks startup: this is cosmetic
// metadata, and no message is at risk if it never finishes.
func backfillAttachmentFlags(ctx context.Context, pool *pgxpool.Pool) {
	repo := postgres.NewAttachmentBackfill(pool)

	done, err := repo.Done(ctx, attachmentBackfillName)
	if err != nil {
		log.Printf("attachment backfill: cannot read the marker, skipping: %v", err)
		return
	}
	if done {
		return
	}

	blobs := diskstorage.NewLocalDiskBlobReader(pool)

	// Bounded: a mailbox with a million read messages should not turn a deploy
	// into an hour of disk reads. What is left is picked up on the next start,
	// because the marker is only written once a pass changes nothing.
	const batch = 5000
	candidates, err := repo.Candidates(ctx, batch)
	if err != nil {
		log.Printf("attachment backfill: cannot list candidates: %v", err)
		return
	}

	corrected := 0
	for _, c := range candidates {
		payload, err := blobs.ReadByID(ctx, c.BlobID)
		if err != nil {
			// A blob that cannot be read is not a reason to stop; the flag
			// stays as it was and the message is untouched.
			continue
		}
		if !usecase.ExtractMessageHeaders(payload).HasAttachments {
			continue
		}
		if err := repo.SetHasAttachments(ctx, c.ID); err != nil {
			log.Printf("attachment backfill: %v", err)
			continue
		}
		corrected++
	}

	if len(candidates) < batch {
		// The whole set fit in one pass, so there is nothing left to do.
		if err := repo.MarkDone(ctx, attachmentBackfillName,
			"corrected single-part attachments"); err != nil {
			log.Printf("attachment backfill: %v", err)
		}
	}
	log.Printf("attachment backfill: examined %d message(s), corrected %d", len(candidates), corrected)
}
