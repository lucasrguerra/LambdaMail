package port

import (
	"context"

	"lambdamail/protocols/internal/domain/entity"
)

type OutboundRepository interface {
	Enqueue(ctx context.Context, job *entity.OutboundJob) error
	FetchNextReady(ctx context.Context, workerID string, limit int) ([]*entity.OutboundJob, error)
	UpdateJob(ctx context.Context, job *entity.OutboundJob) error
}
