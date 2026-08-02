package arc

import (
	"context"
	"crypto"
	"sync"

	"lambdamail/protocols/internal/application/port"
)

// KeyParser turns a stored private key into a signer. It is injected so this
// package stays independent of how DKIM keys are encoded.
type KeyParser func(privateKeyPEM []byte) (crypto.Signer, error)

// LazySealer resolves the signing key on first use instead of at construction.
//
// The composition root builds the sealer before the DKIM provisioner has run,
// so on a first boot no key exists yet. Binding to that empty result would
// disable ARC until the process was restarted by hand - the failure PLAN.md
// section 5 cannot tolerate, because the whole point of ARC is that it is
// always applied, not applied when the ordering happened to work out.
type LazySealer struct {
	keys       port.DkimKeyRepository
	parse      KeyParser
	domain     string
	authServID string

	mu       sync.Mutex
	resolved port.ArcSealer
}

// NewLazySealer returns a sealer that binds to the domain's active DKIM key
// the first time a message needs sealing.
func NewLazySealer(keys port.DkimKeyRepository, parse KeyParser, domain, authServID string) *LazySealer {
	return &LazySealer{keys: keys, parse: parse, domain: domain, authServID: authServID}
}

// Seal delegates to the underlying sealer, resolving it if needed. Until a key
// exists the message is returned untouched: an unsealed message is delivered,
// which is strictly better than a rejected one.
func (l *LazySealer) Seal(ctx context.Context, message []byte, authResult port.InboundAuthResult) ([]byte, error) {
	sealer, err := l.sealer(ctx)
	if err != nil || sealer == nil {
		return message, err
	}
	return sealer.Seal(ctx, message, authResult)
}

func (l *LazySealer) sealer(ctx context.Context) (port.ArcSealer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.resolved != nil {
		return l.resolved, nil
	}

	active, err := l.keys.FindActiveKeys(ctx, l.domain)
	if err != nil {
		return nil, err
	}

	for _, key := range active {
		signer, err := l.parse(key.PrivateKeyPEM)
		if err != nil {
			continue
		}
		l.resolved = NewSealer(l.domain, key.Selector, l.authServID, signer)
		return l.resolved, nil
	}

	// Still no usable key. Leaving resolved nil means the next message tries
	// again, which is what turns "not provisioned yet" into a delay rather
	// than a permanent loss of sealing.
	return nil, nil
}
