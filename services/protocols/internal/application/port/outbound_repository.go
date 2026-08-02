package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/domain/entity"
)

type OutboundRepository interface {
	Enqueue(ctx context.Context, job *entity.OutboundJob) error
	FetchNextReady(ctx context.Context, workerID string, limit int) ([]*entity.OutboundJob, error)
	UpdateJob(ctx context.Context, job *entity.OutboundJob) error

	// CountRecipientsSince reports how many recipients a mailbox has queued
	// since a point in time. It backs the per-mailbox send limit that keeps a
	// compromised account from becoming a spam source (PLAN.md section 5.2).
	CountRecipientsSince(ctx context.Context, mailboxID uuid.UUID, since time.Time) (int, error)
}
