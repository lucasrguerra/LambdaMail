package usecase

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/domain/valueobject"
)

// ErrPolicyDowngradeRefused is returned when a destination's published TLS
// policy could not be satisfied. PLAN.md section 6.2 is explicit: the message
// is deferred with 4.7.5, never delivered over an unvalidated channel.
var ErrPolicyDowngradeRefused = errors.New("outbound: refusing to deliver without the TLS validation the destination requires")

type OutboundWorkerUseCase struct {
	outboundRepo   port.OutboundRepository
	mxResolver     port.MXResolver
	blobReader     port.BlobReader
	inboundUC      *ProcessInboundEmailUseCase
	mailboxes      port.MailboxRepository
	policyResolver port.TLSPolicyResolver
	relay          RelayConfig
	localHost      string
}

// SetRelay routes delivery through a smarthost (PLAN.md section 10.4). This is
// the fallback for hosts whose provider blocks outbound port 25, and it
// replaces MX resolution entirely: every message goes to the relay.
func (w *OutboundWorkerUseCase) SetRelay(relay RelayConfig) {
	w.relay = relay
}

// SetPolicyResolver enables DANE and MTA-STS on the delivery path. Without
// one the worker falls back to opportunistic TLS, which is correct for a
// destination that publishes no policy but must never be the choice for one
// that does.
func (w *OutboundWorkerUseCase) SetPolicyResolver(resolver port.TLSPolicyResolver) {
	w.policyResolver = resolver
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

	// With a smarthost every message goes to the relay regardless of
	// destination, and the relay owns transport security from there on.
	if w.relay.Configured() {
		if err := w.deliverViaRelay(job.EnvelopeFrom, job.EnvelopeTo, payload); err != nil {
			w.finishFailed(ctx, job, err)
			return
		}
		job.Status = entity.OutboundJobStatusDelivered
		job.LastError = ""
		job.LastSmtpCode = ""
		job.TlsPolicyUsed = entity.TLSModeRelay
		_ = w.outboundRepo.UpdateJob(ctx, job)
		return
	}

	mxHosts, err := w.mxResolver.LookupMX(ctx, job.DestinationDomain)
	if err != nil || len(mxHosts) == 0 {
		mxHosts = []string{job.DestinationDomain}
	}

	var lastErr error
	delivered := false
	policyUsed := entity.TLSModeOpportunistic

	for _, mxHost := range mxHosts {
		policy, err := w.resolvePolicy(ctx, job.DestinationDomain, mxHost)
		if err != nil {
			// A published policy we cannot evaluate is a reason to wait, not
			// a reason to send in the clear.
			lastErr = err
			continue
		}

		// RFC 8461 section 4.1: a host outside the policy's mx set must not
		// be used while that policy is in force.
		if policy.RequiresValidation() && !policy.CoversHost(mxHost) {
			lastErr = fmt.Errorf("%w: %s is not listed in the MTA-STS policy of %s",
				ErrPolicyDowngradeRefused, mxHost, job.DestinationDomain)
			continue
		}

		if err := w.deliverToMX(ctx, mxHost, job.EnvelopeFrom, job.EnvelopeTo, payload, policy); err == nil {
			delivered = true
			policyUsed = policy.Effective()
			break
		} else {
			lastErr = err
		}
	}

	if delivered {
		job.Status = entity.OutboundJobStatusDelivered
		job.LastError = ""
		job.LastSmtpCode = ""
		job.TlsPolicyUsed = policyUsed
		_ = w.outboundRepo.UpdateJob(ctx, job)
		return
	}

	w.finishFailed(ctx, job, lastErr)
}

// finishFailed classifies a delivery failure and records the outcome.
func (w *OutboundWorkerUseCase) finishFailed(ctx context.Context, job *entity.OutboundJob, lastErr error) {
	errStr := "delivery failed"
	if lastErr != nil {
		errStr = lastErr.Error()
	}

	// The remote reply code decides the outcome (PLAN.md section 6.3): 5xx is
	// a permanent failure that bounces now, anything else - including a dial
	// or TLS error, which carries no reply code at all - is retried.
	code := smtpReplyCode(lastErr)
	if code > 0 {
		job.LastSmtpCode = strconv.Itoa(code)
	}

	// A refused downgrade is always temporary: the destination may fix its
	// certificate, and bouncing would hand the attacker the outcome they
	// wanted (PLAN.md section 6.2).
	if errors.Is(lastErr, ErrPolicyDowngradeRefused) {
		job.LastSmtpCode = "451"
		job.TlsPolicyUsed = "none"
		w.handleFailure(ctx, job, "4.7.5 "+errStr, false)
		return
	}

	w.handleFailure(ctx, job, errStr, code >= 500 && code < 600)
}

// smtpReplyCode extracts the reply code from an error returned by net/smtp,
// which wraps remote replies in *textproto.Error. It returns 0 when the
// failure happened before any reply was read.
func smtpReplyCode(err error) int {
	var protoErr *textproto.Error
	if errors.As(err, &protoErr) {
		return protoErr.Code
	}
	return 0
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

	dsnBytes, isLoop := valueobject.BuildDsnReport(action, w.localHost, job.EnvelopeFrom, job.EnvelopeTo, job.ID.String(), reason)
	if isLoop || dsnBytes == nil {
		return
	}

	if w.mailboxes == nil {
		return
	}

	targets, err := w.mailboxes.ResolveDeliveryTargets(ctx, job.EnvelopeFrom)
	if err != nil || len(targets) == 0 {
		return
	}

	// Handle indexes the two slices in lockstep, so the envelope address is
	// repeated once per resolved mailbox. An alias sender fans out to several
	// mailboxes under the same address.
	addresses := make([]string, len(targets))
	for i := range targets {
		addresses[i] = job.EnvelopeFrom
	}

	_ = w.inboundUC.Handle(ctx, ProcessInboundEmailInput{
		Sender:             "postmaster@" + w.localHost,
		Recipients:         targets,
		RecipientAddresses: addresses,
		Body:               bytes.NewReader(dsnBytes),
	})
}

// resolvePolicy asks the resolver what transport security the destination
// requires. With no resolver configured every destination is opportunistic.
func (w *OutboundWorkerUseCase) resolvePolicy(ctx context.Context, destinationDomain, mxHost string) (entity.TLSPolicy, error) {
	if w.policyResolver == nil {
		return entity.NewTLSPolicy(false, false), nil
	}
	return w.policyResolver.Resolve(ctx, destinationDomain, mxHost)
}

// tlsConfigFor turns a policy into a TLS configuration.
//
// Opportunistic delivery deliberately skips verification: RFC 7435 prefers an
// unauthenticated encrypted channel to a cleartext one, and the alternative -
// refusing every destination with a self-signed certificate - would lose
// legitimate mail. When a policy is in force the verification is real, and a
// failure surfaces as a refusal to deliver.
func tlsConfigFor(mxHost string, policy entity.TLSPolicy) *tls.Config {
	config := &tls.Config{
		ServerName: strings.TrimSuffix(mxHost, "."),
		MinVersion: tls.VersionTLS12,
	}

	if !policy.RequiresValidation() {
		config.InsecureSkipVerify = true
		return config
	}

	if policy.Effective() == entity.TLSModeDane {
		// DANE authenticates the certificate directly against the
		// DNSSEC-signed association, so the PKIX chain and the name are not
		// what is being trusted (RFC 7672 section 3.1). Verification is done
		// by hand below instead of by the standard chain builder.
		config.InsecureSkipVerify = true
		config.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			chain := make([]*x509.Certificate, 0, len(rawCerts))
			for _, raw := range rawCerts {
				cert, err := x509.ParseCertificate(raw)
				if err != nil {
					return fmt.Errorf("parse peer certificate: %w", err)
				}
				chain = append(chain, cert)
			}
			if !policy.MatchesCertificate(chain) {
				return fmt.Errorf("%w: certificate of %s matches no TLSA record", ErrPolicyDowngradeRefused, mxHost)
			}
			return nil
		}
		return config
	}

	// MTA-STS in enforce mode requires a normal, publicly trusted PKIX
	// validation against the MX name (RFC 8461 section 4.1).
	return config
}

func (w *OutboundWorkerUseCase) deliverToMX(ctx context.Context, mxHost string, from string, to string, payload []byte, policy entity.TLSPolicy) error {
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

	starttlsOffered, _ := client.Extension("STARTTLS")
	if starttlsOffered {
		if err := client.StartTLS(tlsConfigFor(mxHost, policy)); err != nil {
			if policy.RequiresValidation() {
				return fmt.Errorf("%w: STARTTLS to %s failed validation: %v", ErrPolicyDowngradeRefused, mxHost, err)
			}
			return fmt.Errorf("STARTTLS failed: %w", err)
		}
	} else if policy.RequiresValidation() {
		// The destination's own published policy says it speaks TLS. A peer
		// that does not offer STARTTLS is either broken or being stripped,
		// and handing it the message either way is exactly the downgrade the
		// policy exists to prevent.
		return fmt.Errorf("%w: %s did not offer STARTTLS", ErrPolicyDowngradeRefused, mxHost)
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
