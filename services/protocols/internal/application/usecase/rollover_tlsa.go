package usecase

import (
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"time"
)

// RolloverStage names the state the rollover is in. It follows the state
// machine of PLAN.md section 5.1, which exists because the naive approach -
// publish the digest of the current certificate and update it at renewal -
// permanently breaks mail delivery to every DANE-validating receiver for as
// long as DNS has not caught up.
type RolloverStage string

const (
	// StageStable means one association is published and the certificate
	// matches it. Nothing to do.
	StageStable RolloverStage = "STABLE"
	// StagePublishNext means a key for the next certificate has been
	// generated and its association has to go into DNS.
	StagePublishNext RolloverStage = "PUBLISH_NEXT"
	// StageAwaitPropagation means both associations are published and the
	// rollover is waiting out the DNS caches before switching over.
	StageAwaitPropagation RolloverStage = "AWAIT_PROPAGATION"
	// StageRenew means the wait is over and the certificate can be re-issued
	// against the pre-generated key.
	StageRenew RolloverStage = "RENEW"
	// StageCleanup means the new certificate is live and the old association
	// can be retired, again only after the caches have expired.
	StageCleanup RolloverStage = "CLEANUP"
)

// RolloverConfig tunes the timings.
type RolloverConfig struct {
	// RenewBefore is how long before expiry a renewal starts. Let's Encrypt
	// certificates last 90 days and are renewable from day 60.
	RenewBefore time.Duration
	// PrepareBefore is how long before the renewal the next key is generated
	// and published. It has to exceed PropagationWait comfortably.
	PrepareBefore time.Duration
	// PropagationWait is how long both associations stay published before the
	// certificate changes. RFC 7671 section 8.1 asks for at least twice the
	// record TTL; a day is used because a resolver that ignores TTLs is not
	// hypothetical.
	PropagationWait time.Duration
}

// DefaultRolloverConfig follows PLAN.md section 5.1.
func DefaultRolloverConfig() RolloverConfig {
	return RolloverConfig{
		RenewBefore:     30 * 24 * time.Hour,
		PrepareBefore:   30 * 24 * time.Hour,
		PropagationWait: 24 * time.Hour,
	}
}

// RolloverState is everything the decision needs.
type RolloverState struct {
	// CurrentDigest is the TLSA value of the certificate being served.
	CurrentDigest string
	// NotAfter is the expiry of the certificate being served.
	NotAfter time.Time
	// PendingDigest is the association of the pre-generated next key, empty
	// when no rollover is in flight.
	PendingDigest string
	// PendingPublishedAt is when that association went into DNS.
	PendingPublishedAt *time.Time
	// CertificateMatchesPending is true once the served certificate was
	// issued against the pending key.
	CertificateMatchesPending bool
}

// RolloverDecision is the action to take now.
type RolloverDecision struct {
	Stage RolloverStage
	// Publish is the full set of associations that must be in DNS after this
	// step. It is always the complete desired state, never a delta, so
	// applying it is idempotent.
	Publish []string
	Reason  string
}

// RolloverTlsaUseCase decides how to move a DANE rollover forward. It is a
// pure decision function over the observed state: the caller performs the DNS
// writes and the issuance, which keeps the risky ordering rules in one
// testable place.
type RolloverTlsaUseCase struct {
	config RolloverConfig
	now    func() time.Time
}

func NewRolloverTlsaUseCase(config RolloverConfig) *RolloverTlsaUseCase {
	if config.RenewBefore == 0 {
		config = DefaultRolloverConfig()
	}
	return &RolloverTlsaUseCase{config: config, now: time.Now}
}

// WithClock fixes the clock for tests.
func (uc *RolloverTlsaUseCase) WithClock(now func() time.Time) *RolloverTlsaUseCase {
	uc.now = now
	return uc
}

func (uc *RolloverTlsaUseCase) Decide(_ context.Context, state RolloverState) RolloverDecision {
	now := uc.now()

	// The new certificate is live. Both associations are still published;
	// the old one may only go once the caches that hold it have expired.
	if state.CertificateMatchesPending {
		if state.PendingPublishedAt != nil && now.Sub(*state.PendingPublishedAt) < uc.config.PropagationWait {
			return RolloverDecision{
				Stage:   StageCleanup,
				Publish: dedupe(state.CurrentDigest, state.PendingDigest),
				Reason:  "new certificate is live; keeping the previous association until caches expire",
			}
		}
		return RolloverDecision{
			Stage:   StageStable,
			Publish: dedupe(state.PendingDigest),
			Reason:  "rollover complete; the previous association can be removed",
		}
	}

	// A rollover already in flight: both are published, so the only question
	// is whether the wait is over.
	if state.PendingDigest != "" {
		if state.PendingPublishedAt == nil {
			return RolloverDecision{
				Stage:   StagePublishNext,
				Publish: dedupe(state.CurrentDigest, state.PendingDigest),
				Reason:  "publishing the association for the pre-generated next key",
			}
		}
		if now.Sub(*state.PendingPublishedAt) < uc.config.PropagationWait {
			remaining := uc.config.PropagationWait - now.Sub(*state.PendingPublishedAt)
			return RolloverDecision{
				Stage:   StageAwaitPropagation,
				Publish: dedupe(state.CurrentDigest, state.PendingDigest),
				Reason:  fmt.Sprintf("waiting %s more for the next association to propagate", remaining.Round(time.Minute)),
			}
		}
		return RolloverDecision{
			Stage:   StageRenew,
			Publish: dedupe(state.CurrentDigest, state.PendingDigest),
			Reason:  "the next association has propagated; the certificate can be issued against the pre-generated key",
		}
	}

	// No rollover in flight. Start one only when the renewal is close enough.
	if !state.NotAfter.IsZero() && now.After(state.NotAfter.Add(-uc.config.PrepareBefore)) {
		return RolloverDecision{
			Stage:   StagePublishNext,
			Publish: dedupe(state.CurrentDigest),
			Reason:  "renewal is approaching; a next key has to be generated and published",
		}
	}

	return RolloverDecision{
		Stage:   StageStable,
		Publish: dedupe(state.CurrentDigest),
		Reason:  "certificate is current and no rollover is due",
	}
}

// ShouldRenewNow reports whether the certificate itself is due for renewal,
// independently of DANE. Deployments without DANE use only this.
func (uc *RolloverTlsaUseCase) ShouldRenewNow(notAfter time.Time) bool {
	if notAfter.IsZero() {
		return true
	}
	return uc.now().After(notAfter.Add(-uc.config.RenewBefore))
}

// TlsaDigestForKey renders the "3 1 1" association of a private key: the
// SHA-256 of its SubjectPublicKeyInfo (RFC 6698 section 2.1.2). Deriving it
// from the key rather than from a certificate is what lets the association be
// published before the certificate exists.
func TlsaDigestForKey(privateKeyPEM []byte) (string, error) {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return "", fmt.Errorf("rollover: private key is not valid PEM")
	}

	var publicKey any
	switch parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); {
	case err == nil:
		// crypto.Signer is the interface every usable private key satisfies;
		// its Public method returns crypto.PublicKey, which is a defined type
		// rather than a bare any, so it has to be named exactly.
		signer, ok := parsed.(crypto.Signer)
		if !ok {
			return "", fmt.Errorf("rollover: key of type %T has no public half", parsed)
		}
		publicKey = signer.Public()
	default:
		ecKey, ecErr := x509.ParseECPrivateKey(block.Bytes)
		if ecErr != nil {
			return "", fmt.Errorf("rollover: parse private key: %w", err)
		}
		publicKey = &ecKey.PublicKey
	}

	spki, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("rollover: marshal public key: %w", err)
	}

	sum := sha256.Sum256(spki)
	return hex.EncodeToString(sum[:]), nil
}

// TlsaDigestForCertificate renders the association of an already issued
// certificate, used to confirm what is actually being served.
func TlsaDigestForCertificate(certificatePEM []byte) (string, error) {
	block, _ := pem.Decode(certificatePEM)
	if block == nil {
		return "", fmt.Errorf("rollover: certificate is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("rollover: parse certificate: %w", err)
	}

	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), nil
}

func dedupe(values ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
