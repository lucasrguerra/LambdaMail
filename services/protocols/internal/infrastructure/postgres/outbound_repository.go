package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/domain/entity"
)

type OutboundRepository struct {
	pool *pgxpool.Pool
}

func NewOutboundRepository(pool *pgxpool.Pool) *OutboundRepository {
	return &OutboundRepository{pool: pool}
}

func (r *OutboundRepository) Enqueue(ctx context.Context, job *entity.OutboundJob) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO outbound_jobs (
			mailbox_id, blob_id, envelope_from, envelope_to, destination_domain,
			status, attempt, next_attempt_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at
	`, job.MailboxID, job.BlobID, job.EnvelopeFrom, job.EnvelopeTo, job.DestinationDomain,
		job.Status, job.Attempt, job.NextAttemptAt, job.ExpiresAt).Scan(&job.ID, &job.CreatedAt)

	if err != nil {
		return fmt.Errorf("enqueue outbound job: %w", err)
	}
	return nil
}

func (r *OutboundRepository) FetchNextReady(ctx context.Context, workerID string, limit int) ([]*entity.OutboundJob, error) {
	rows, err := r.pool.Query(ctx, `
		WITH ready_jobs AS (
			SELECT id FROM outbound_jobs
			WHERE (
			        (status IN ('QUEUED', 'DEFERRED') AND next_attempt_at <= NOW())
			        -- Reclaim jobs whose worker died mid-delivery. Without this
			        -- they stay SENDING forever and the message is silently
			        -- lost, which contradicts ADR-002's durability guarantee.
			     OR (status = 'SENDING' AND locked_until IS NOT NULL AND locked_until <= NOW())
			      )
			ORDER BY next_attempt_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outbound_jobs
		SET status = 'SENDING', locked_by = $1, locked_until = NOW() + INTERVAL '5 minutes'
		FROM ready_jobs
		WHERE outbound_jobs.id = ready_jobs.id
		RETURNING outbound_jobs.id, outbound_jobs.mailbox_id, outbound_jobs.blob_id,
		          outbound_jobs.envelope_from, outbound_jobs.envelope_to, outbound_jobs.destination_domain,
		          outbound_jobs.status, outbound_jobs.attempt, outbound_jobs.next_attempt_at,
		          outbound_jobs.expires_at, outbound_jobs.last_smtp_code, outbound_jobs.last_error,
		          outbound_jobs.tls_policy_used, outbound_jobs.delay_dsn_sent, outbound_jobs.created_at
	`, workerID, limit)

	if err != nil {
		return nil, fmt.Errorf("fetch ready outbound jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*entity.OutboundJob
	for rows.Next() {
		j := &entity.OutboundJob{}
		var statusStr string
		var lastSmtpCode, lastError, tlsPolicyUsed *string

		if err := rows.Scan(
			&j.ID, &j.MailboxID, &j.BlobID, &j.EnvelopeFrom, &j.EnvelopeTo, &j.DestinationDomain,
			&statusStr, &j.Attempt, &j.NextAttemptAt, &j.ExpiresAt, &lastSmtpCode, &lastError,
			&tlsPolicyUsed, &j.DelayDsnSent, &j.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan outbound job: %w", err)
		}

		j.Status = entity.OutboundJobStatus(statusStr)
		if lastSmtpCode != nil {
			j.LastSmtpCode = *lastSmtpCode
		}
		if lastError != nil {
			j.LastError = *lastError
		}
		if tlsPolicyUsed != nil {
			j.TlsPolicyUsed = *tlsPolicyUsed
		}

		jobs = append(jobs, j)
	}

	return jobs, nil
}

// CountRecipientsSince counts queued recipients, not messages: one message to
// fifty addresses is fifty recipients against the limit, which is the number
// that matters for abuse.
func (r *OutboundRepository) CountRecipientsSince(ctx context.Context, mailboxID uuid.UUID, since time.Time) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM outbound_jobs
		WHERE mailbox_id = $1 AND created_at >= $2
	`, mailboxID, since).Scan(&count)

	if err != nil {
		return 0, fmt.Errorf("count recent recipients for mailbox %s: %w", mailboxID, err)
	}
	return count, nil
}

func (r *OutboundRepository) UpdateJob(ctx context.Context, job *entity.OutboundJob) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE outbound_jobs
		SET status = $1, attempt = $2, next_attempt_at = $3, last_smtp_code = $4,
		    last_error = $5, tls_policy_used = $6, delay_dsn_sent = $7, locked_by = NULL, locked_until = NULL
		WHERE id = $8
	`, job.Status, job.Attempt, job.NextAttemptAt, job.LastSmtpCode,
		job.LastError, job.TlsPolicyUsed, job.DelayDsnSent, job.ID)

	if err != nil {
		return fmt.Errorf("update outbound job %s: %w", job.ID, err)
	}
	return nil
}
