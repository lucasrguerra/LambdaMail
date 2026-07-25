package usecase

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

type fakeOutboundRepo struct {
	jobs []*entity.OutboundJob
}

func (f *fakeOutboundRepo) Enqueue(_ context.Context, job *entity.OutboundJob) error {
	f.jobs = append(f.jobs, job)
	return nil
}

func (f *fakeOutboundRepo) FetchNextReady(_ context.Context, _ string, _ int) ([]*entity.OutboundJob, error) {
	var ready []*entity.OutboundJob
	for _, j := range f.jobs {
		if j.Status == entity.OutboundJobStatusQueued || j.Status == entity.OutboundJobStatusDeferred {
			ready = append(ready, j)
		}
	}
	return ready, nil
}

func (f *fakeOutboundRepo) UpdateJob(_ context.Context, job *entity.OutboundJob) error {
	for i, j := range f.jobs {
		if j.ID == job.ID {
			f.jobs[i] = job
			break
		}
	}
	return nil
}

type fakeMXResolver struct {
	hosts []string
}

func (f *fakeMXResolver) LookupMX(_ context.Context, _ string) ([]string, error) {
	return f.hosts, nil
}

type fakeBlobReader struct {
	payload []byte
}

func (f *fakeBlobReader) ReadByID(_ context.Context, _ uuid.UUID) ([]byte, error) {
	return f.payload, nil
}

func TestOutboundWorker_DeferralAndPermanentFailure(t *testing.T) {
	repo := &fakeOutboundRepo{}
	mx := &fakeMXResolver{hosts: []string{"127.0.0.1:9999"}} // unreachable port -> connection error
	blob := &fakeBlobReader{payload: []byte("From: a\r\nTo: b\r\n\r\nTest")}

	mbID := uuid.New()
	job := &entity.OutboundJob{
		ID:                uuid.New(),
		MailboxID:         &mbID,
		BlobID:            uuid.New(),
		EnvelopeFrom:      "user@domain.test",
		EnvelopeTo:        "rcpt@remote.test",
		DestinationDomain: "remote.test",
		Status:            entity.OutboundJobStatusQueued,
		ExpiresAt:         time.Now().Add(5 * 24 * time.Hour),
	}
	repo.Enqueue(context.Background(), job)

	worker := NewOutboundWorkerUseCase(repo, mx, blob, nil, nil, "mail.local")
	processed, err := worker.ProcessBatch(context.Background(), "w1", 10)
	if err != nil || processed != 1 {
		t.Fatalf("ProcessBatch failed: processed=%d, err=%v", processed, err)
	}

	if repo.jobs[0].Status != entity.OutboundJobStatusDeferred {
		t.Errorf("expected DEFERRED status, got %s", repo.jobs[0].Status)
	}
	if repo.jobs[0].Attempt != 1 {
		t.Errorf("attempt count = %d, want 1", repo.jobs[0].Attempt)
	}
	if !strings.Contains(repo.jobs[0].LastError, "dial tcp") {
		t.Errorf("unexpected error message: %s", repo.jobs[0].LastError)
	}
}
