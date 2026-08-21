package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// Reports that arrived before ingestion existed are still sitting in the
// admin's inbox. They are ordinary stored messages, so they can be read back
// out and parsed after the fact.

type stubDeliveredReportStore struct {
	pending []port.StoredReportMessage
	filed   []uuid.UUID
	blobs   map[uuid.UUID][]byte
	listErr error
	fileErr error
	blobErr error
}

func (s *stubDeliveredReportStore) ListUningestedReports(
	_ context.Context, limit int,
) ([]port.StoredReportMessage, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if limit > 0 && len(s.pending) > limit {
		return s.pending[:limit], nil
	}
	return s.pending, nil
}

func (s *stubDeliveredReportStore) MoveToReportsFolder(_ context.Context, messageID uuid.UUID) error {
	if s.fileErr != nil {
		return s.fileErr
	}
	s.filed = append(s.filed, messageID)
	return nil
}

func (s *stubDeliveredReportStore) ReadByID(_ context.Context, blobID uuid.UUID) ([]byte, error) {
	if s.blobErr != nil {
		return nil, s.blobErr
	}
	body, ok := s.blobs[blobID]
	if !ok {
		return nil, errors.New("blob not found")
	}
	return body, nil
}

func newBackfill(store *stubDeliveredReportStore, repo *recordingReportRepo) *BackfillReportsUseCase {
	return NewBackfillReportsUseCase(
		store, store, NewIngestDeliveredReportsUseCase(NewIngestReportsUseCase(repo)))
}

func TestBackfillIngestsAReportAlreadyInTheMailbox(t *testing.T) {
	blobID := uuid.New()
	messageID := uuid.New()
	payload := messageWithAttachment(googleTlsRptFilename, "application/gzip", gzipped(t, tlsRptJSON))

	store := &stubDeliveredReportStore{
		pending: []port.StoredReportMessage{
			{MessageID: messageID, BlobID: blobID, RecipientAddress: "tlsrpt@example.test"},
		},
		blobs: map[uuid.UUID][]byte{blobID: payload},
	}
	repo := &recordingReportRepo{}

	summary, err := newBackfill(store, repo).Run(context.Background(), 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(repo.tlsRpt) != 1 {
		t.Fatalf("the stored report was not ingested: %d", len(repo.tlsRpt))
	}
	if repo.tlsRpt[0].Domain != "example.test" {
		t.Errorf("ingested domain is %q", repo.tlsRpt[0].Domain)
	}
	if summary.Ingested != 1 {
		t.Errorf("summary reports %d ingested, want 1", summary.Ingested)
	}
	// Filed out of the inbox, which is the point of running this at all.
	if len(store.filed) != 1 || store.filed[0] != messageID {
		t.Errorf("the message was not moved to Reports: %v", store.filed)
	}
}

// Running it twice must be safe: the second pass has nothing left to do, and
// storage is idempotent anyway.
func TestBackfillOverAnEmptyBacklogDoesNothing(t *testing.T) {
	store := &stubDeliveredReportStore{}
	repo := &recordingReportRepo{}

	summary, err := newBackfill(store, repo).Run(context.Background(), 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if summary.Ingested != 0 || summary.Failed != 0 {
		t.Errorf("an empty backlog produced %+v", summary)
	}
}

// One unreadable report must not stop the rest of the backlog: a single
// corrupted attachment from years ago would otherwise block every report
// behind it forever.
func TestBackfillContinuesPastAnUnreadableReport(t *testing.T) {
	goodBlob, badBlob := uuid.New(), uuid.New()
	goodMsg, badMsg := uuid.New(), uuid.New()

	store := &stubDeliveredReportStore{
		pending: []port.StoredReportMessage{
			{MessageID: badMsg, BlobID: badBlob, RecipientAddress: "tlsrpt@example.test"},
			{MessageID: goodMsg, BlobID: goodBlob, RecipientAddress: "tlsrpt@example.test"},
		},
		blobs: map[uuid.UUID][]byte{
			badBlob:  messageWithAttachment("broken.json.gz", "application/gzip", []byte("rubbish")),
			goodBlob: messageWithAttachment(googleTlsRptFilename, "application/gzip", gzipped(t, tlsRptJSON)),
		},
	}
	repo := &recordingReportRepo{}

	summary, err := newBackfill(store, repo).Run(context.Background(), 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if summary.Ingested != 1 {
		t.Errorf("the readable report was not ingested: %+v", summary)
	}
	if summary.Failed != 1 {
		t.Errorf("the unreadable one was not reported: %+v", summary)
	}
	// Both are filed either way - the inbox is being cleared, and a report
	// this server cannot parse is still a report.
	if len(store.filed) != 2 {
		t.Errorf("filed %d messages, want both", len(store.filed))
	}
}

// A blob that cannot be read at all is counted and skipped, never fatal.
func TestBackfillSurvivesAMissingBlob(t *testing.T) {
	store := &stubDeliveredReportStore{
		pending: []port.StoredReportMessage{
			{MessageID: uuid.New(), BlobID: uuid.New(), RecipientAddress: "dmarc@example.test"},
		},
		blobs: map[uuid.UUID][]byte{},
	}
	repo := &recordingReportRepo{}

	summary, err := newBackfill(store, repo).Run(context.Background(), 0)
	if err != nil {
		t.Fatalf("a missing blob must not fail the run: %v", err)
	}
	if summary.Failed != 1 {
		t.Errorf("the missing blob was not counted: %+v", summary)
	}
}

// A failure listing the backlog is the one thing worth stopping for: it means
// the database is unreachable, and every message would be counted as failed.
func TestBackfillStopsIfItCannotReadTheBacklog(t *testing.T) {
	store := &stubDeliveredReportStore{listErr: errors.New("database is down")}
	if _, err := newBackfill(store, &recordingReportRepo{}).Run(context.Background(), 0); err == nil {
		t.Error("a backlog that cannot be listed should be reported as an error")
	}
}
