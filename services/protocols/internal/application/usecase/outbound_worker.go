package usecase

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/domain/valueobject"
)

type OutboundWorkerUseCase struct {
	outboundRepo port.OutboundRepository
	mxResolver   port.MXResolver
	blobReader   port.BlobReader
	inboundUC    *ProcessInboundEmailUseCase
	mailboxes    port.MailboxRepository
	localHost    string
}

func NewOutboundWorkerUseCase(
	outboundRepo port.OutboundRepository,
	mxResolver port.MXResolver,
	blobReader port.BlobReader,
	inboundUC *ProcessInboundEmailUseCase,
	mailboxes port.MailboxRepository,
	localHost string,
) *OutboundWorkerUseCase {
	if localHost == "" {
		localHost = "mail.lambdamail.local"
	}
	return &OutboundWorkerUseCase{
		outboundRepo: outboundRepo,
		mxResolver:   mxResolver,
		blobReader:   blobReader,
		inboundUC:    inboundUC,
		mailboxes:    mailboxes,
		localHost:    localHost,
	}
}

func (w *OutboundWorkerUseCase) ProcessBatch(ctx context.Context, workerID string, limit int) (int, error) {
	if limit <= 0 {
		limit = 10
	}
	jobs, err := w.outboundRepo.FetchNextReady(ctx, workerID, limit)
	if err != nil {
		return 0, fmt.Errorf("fetch ready jobs: %w", err)
	}

	processed := 0
	for _, job := range jobs {
		w.processSingleJob(ctx, job)
		processed++
	}

	return processed, nil
}

func (w *OutboundWorkerUseCase) processSingleJob(ctx context.Context, job *entity.OutboundJob) {
	job.Attempt++

	payload, err := w.blobReader.ReadByID(ctx, job.BlobID)
	if err != nil {
		w.handleFailure(ctx, job, fmt.Sprintf("read blob %s: %v", job.BlobID, err), true)
		return
	}

	mxHosts, err := w.mxResolver.LookupMX(ctx, job.DestinationDomain)
	if err != nil || len(mxHosts) == 0 {
		mxHosts = []string{job.DestinationDomain}
	}

	var lastErr error
	delivered := false

	for _, mxHost := range mxHosts {
		if err := w.deliverToMX(ctx, mxHost, job.EnvelopeFrom, job.EnvelopeTo, payload); err == nil {
			delivered = true
			break
		} else {
			lastErr = err
		}
	}

	if delivered {
		job.Status = entity.OutboundJobStatusDelivered
		job.LastError = ""
		job.TlsPolicyUsed = "opportunistic"
		_ = w.outboundRepo.UpdateJob(ctx, job)
		return
	}

	errStr := "delivery failed"
	if lastErr != nil {
		errStr = lastErr.Error()
	}

	isPermanent := strings.HasPrefix(errStr, "5")
	w.handleFailure(ctx, job, errStr, isPermanent)
}

func (w *OutboundWorkerUseCase) handleFailure(ctx context.Context, job *entity.OutboundJob, errStr string, isPermanent bool) {
	now := time.Now()
	nextAttemptAt, sendDelayDsn, maxExceeded := entity.CalculateNextBackoff(now, job.Attempt, job.ExpiresAt)

	if isPermanent || maxExceeded {
		job.Status = entity.OutboundJobStatusBounced
		job.LastError = errStr
		_ = w.outboundRepo.UpdateJob(ctx, job)

		w.sendDsn(ctx, valueobject.DsnActionFailed, job, errStr)
		return
	}

	job.Status = entity.OutboundJobStatusDeferred
	job.NextAttemptAt = nextAttemptAt
	job.LastError = errStr

	if sendDelayDsn && !job.DelayDsnSent {
		w.sendDsn(ctx, valueobject.DsnActionDelayed, job, errStr)
		job.DelayDsnSent = true
	}

	_ = w.outboundRepo.UpdateJob(ctx, job)
}

func (w *OutboundWorkerUseCase) sendDsn(ctx context.Context, action valueobject.DsnAction, job *entity.OutboundJob, reason string) {
	if w.inboundUC == nil || job.MailboxID == nil {
		return
	}

	dsnBytes, isLoop := valueobject.BuildDsnReport(action, job.EnvelopeFrom, job.EnvelopeTo, job.ID.String(), reason)
	if isLoop || dsnBytes == nil {
		return
	}

	if w.mailboxes != nil {
		targets, err := w.mailboxes.ResolveDeliveryTargets(ctx, job.EnvelopeFrom)
		if err == nil && len(targets) > 0 {
			_ = w.inboundUC.Handle(ctx, ProcessInboundEmailInput{
				Sender:             "postmaster@lambdamail.local",
				Recipients:         targets,
				RecipientAddresses: []string{job.EnvelopeFrom},
				Body:               bytes.NewReader(dsnBytes),
			})
		}
	}
}

func (w *OutboundWorkerUseCase) deliverToMX(ctx context.Context, mxHost string, from string, to string, payload []byte) error {
	addr := mxHost
	if _, _, err := net.SplitHostPort(mxHost); err != nil {
		addr = net.JoinHostPort(mxHost, "25")
	}
	d := net.Dialer{Timeout: 10 * time.Second}

	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial tcp %s: %w", addr, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, mxHost)
	if err != nil {
		return fmt.Errorf("smtp client handshake: %w", err)
	}
	defer client.Close()

	if err := client.Hello(w.localHost); err != nil {
		return fmt.Errorf("EHLO failed: %w", err)
	}

	if ok, _ := client.Extension("STARTTLS"); ok {
		config := &tls.Config{InsecureSkipVerify: true, ServerName: mxHost}
		if err := client.StartTLS(config); err != nil {
			return fmt.Errorf("STARTTLS failed: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM failed: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO failed: %w", err)
	}

	wWriter, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command failed: %w", err)
	}

	if _, err := wWriter.Write(payload); err != nil {
		wWriter.Close()
		return fmt.Errorf("write DATA payload failed: %w", err)
	}

	if err := wWriter.Close(); err != nil {
		return fmt.Errorf("close DATA writer failed: %w", err)
	}

	return client.Quit()
}
