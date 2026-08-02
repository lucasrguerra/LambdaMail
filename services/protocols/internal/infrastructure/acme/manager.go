package acme

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

// TlsaPublisher publishes the DANE associations that must be live for the
// mail host. The rollover hands it the complete desired set, so applying it is
// idempotent.
type TlsaPublisher interface {
	PublishTlsa(ctx context.Context, mailHost string, digests []string) error
}

// PendingKeyStore is the part of the ACME repository the rollover needs
// beyond the plain port: it tracks when an association was published.
type PendingKeyStore interface {
	SavePendingKeyWithDigest(ctx context.Context, domain string, privateKeyPEM []byte, tlsaDigest string) error
	PendingKeyDigest(ctx context.Context, domain string) (string, *time.Time, error)
	MarkPendingKeyPublished(ctx context.Context, domain string) error
}

// Manager keeps the mail host's certificate current and, when DANE is on,
// drives the rollover so the published associations always cover whatever is
// being served.
type Manager struct {
	issuer   *Issuer
	store    port.CertificateStore
	pending  PendingKeyStore
	rollover *usecase.RolloverTlsaUseCase
	tlsa     TlsaPublisher

	mailHost    string
	extraHosts  []string
	daneEnabled bool

	current atomic.Pointer[tls.Certificate]
}

func NewManager(
	issuer *Issuer,
	store port.CertificateStore,
	pending PendingKeyStore,
	tlsa TlsaPublisher,
	mailHost string,
	extraHosts []string,
	daneEnabled bool,
) *Manager {
	return &Manager{
		issuer:      issuer,
		store:       store,
		pending:     pending,
		rollover:    usecase.NewRolloverTlsaUseCase(usecase.DefaultRolloverConfig()),
		tlsa:        tlsa,
		mailHost:    mailHost,
		extraHosts:  extraHosts,
		daneEnabled: daneEnabled,
	}
}

// GetCertificate satisfies tls.Config.GetCertificate.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := m.current.Load()
	if cert == nil {
		return nil, fmt.Errorf("acme: no certificate available for %s yet", m.mailHost)
	}
	return cert, nil
}

// Start loads whatever is stored, issues a certificate if there is none, and
// then keeps reconciling on an interval.
func (m *Manager) Start(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 6 * time.Hour
	}

	if err := m.reconcile(ctx); err != nil {
		return err
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.reconcile(ctx); err != nil {
					log.Printf("acme reconcile failed: %v", err)
				}
			}
		}
	}()

	return nil
}

// reconcile is the whole certificate lifecycle in one pass. It is written to
// be safe to run at any moment: each step only acts when its precondition
// holds, so an interrupted rollover simply resumes on the next tick.
func (m *Manager) reconcile(ctx context.Context) error {
	stored, err := m.store.LoadCertificate(ctx, m.mailHost)
	if err != nil {
		return fmt.Errorf("load stored certificate: %w", err)
	}

	if stored != nil {
		if err := m.serve(stored); err != nil {
			return err
		}
	}

	if !m.daneEnabled {
		// Without DANE there is no association to coordinate with, so the
		// only question is whether the certificate needs renewing.
		if stored == nil || m.rollover.ShouldRenewNow(stored.NotAfter) {
			return m.issue(ctx, nil, 1)
		}
		return nil
	}

	return m.reconcileWithDane(ctx, stored)
}

func (m *Manager) reconcileWithDane(ctx context.Context, stored *port.StoredCertificate) error {
	// A deployment with DANE and no certificate yet: issue against a
	// pre-generated key and publish its association immediately. There is no
	// old association to preserve, so no window to protect.
	if stored == nil {
		keyPEM, err := GeneratePrivateKey()
		if err != nil {
			return err
		}
		digest, err := usecase.TlsaDigestForKey(keyPEM)
		if err != nil {
			return err
		}
		if err := m.tlsa.PublishTlsa(ctx, m.mailHost, []string{digest}); err != nil {
			return fmt.Errorf("publish initial TLSA: %w", err)
		}
		return m.issue(ctx, keyPEM, 1)
	}

	currentDigest, err := usecase.TlsaDigestForCertificate(stored.CertificatePEM)
	if err != nil {
		return err
	}

	pendingDigest, publishedAt, err := m.pending.PendingKeyDigest(ctx, m.mailHost)
	if err != nil {
		return err
	}

	decision := m.rollover.Decide(ctx, usecase.RolloverState{
		CurrentDigest:             currentDigest,
		NotAfter:                  stored.NotAfter,
		PendingDigest:             pendingDigest,
		PendingPublishedAt:        publishedAt,
		CertificateMatchesPending: pendingDigest != "" && pendingDigest == currentDigest,
	})

	log.Printf("acme rollover for %s: %s (%s)", m.mailHost, decision.Stage, decision.Reason)

	// The published set is always applied first. Publishing is additive and
	// idempotent, so doing it before anything else means the associations are
	// never behind the certificate.
	if err := m.tlsa.PublishTlsa(ctx, m.mailHost, decision.Publish); err != nil {
		return fmt.Errorf("publish TLSA set: %w", err)
	}

	switch decision.Stage {
	case usecase.StagePublishNext:
		if pendingDigest == "" {
			// Generate the next key and publish its association. The
			// certificate is not touched on this pass: the association has to
			// propagate first.
			keyPEM, err := GeneratePrivateKey()
			if err != nil {
				return err
			}
			digest, err := usecase.TlsaDigestForKey(keyPEM)
			if err != nil {
				return err
			}
			if err := m.pending.SavePendingKeyWithDigest(ctx, m.mailHost, keyPEM, digest); err != nil {
				return err
			}
			if err := m.tlsa.PublishTlsa(ctx, m.mailHost, []string{currentDigest, digest}); err != nil {
				return fmt.Errorf("publish next TLSA: %w", err)
			}
		}
		return m.pending.MarkPendingKeyPublished(ctx, m.mailHost)

	case usecase.StageRenew:
		keyPEM, err := m.store.LoadPendingKey(ctx, m.mailHost)
		if err != nil {
			return err
		}
		if keyPEM == nil {
			return fmt.Errorf("acme: rollover reached RENEW with no pending key for %s", m.mailHost)
		}
		return m.issue(ctx, keyPEM, stored.KeyGeneration+1)

	case usecase.StageStable:
		// The rollover finished and the old association has been dropped, so
		// the reserved key is no longer pending.
		if pendingDigest != "" && pendingDigest == currentDigest {
			return m.store.ClearPendingKey(ctx, m.mailHost)
		}
		return nil

	default:
		// AWAIT_PROPAGATION and CLEANUP are waits: the published set above is
		// the entire action.
		return nil
	}
}

// issue obtains a certificate and starts serving it.
func (m *Manager) issue(ctx context.Context, privateKeyPEM []byte, generation int) error {
	domains := append([]string{m.mailHost}, m.extraHosts...)

	issued, err := m.issuer.Obtain(ctx, domains, privateKeyPEM)
	if err != nil {
		return err
	}
	issued.KeyGeneration = generation

	if err := m.store.SaveCertificate(ctx, *issued); err != nil {
		return fmt.Errorf("store issued certificate: %w", err)
	}

	log.Printf("acme issued a certificate for %s, valid until %s", strings.Join(domains, ", "), issued.NotAfter.Format(time.RFC3339))
	return m.serve(issued)
}

// serve parses a stored certificate and swaps it in atomically, so in-flight
// handshakes are unaffected.
func (m *Manager) serve(stored *port.StoredCertificate) error {
	pair, err := tls.X509KeyPair(stored.CertificatePEM, stored.PrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("acme: stored certificate and key do not match: %w", err)
	}
	m.current.Store(&pair)
	return nil
}

// Ensure port interface compliance
var _ port.CertProvider = (*Manager)(nil)
