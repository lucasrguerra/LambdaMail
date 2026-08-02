// Package tlspolicy resolves the outbound transport security policy of
// PLAN.md section 6.2: DANE when the destination publishes DNSSEC-signed TLSA
// records, MTA-STS when it publishes a policy over HTTPS, opportunistic TLS
// otherwise.
package tlspolicy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/domain/valueobject"
)

// policyFetchLimit bounds the policy body. RFC 8461 policies are a few
// hundred bytes; anything larger is a resource-exhaustion attempt.
const policyFetchLimit = 64 * 1024

// Resolver implements port.TLSPolicyResolver.
type Resolver struct {
	tlsa       TlsaLookup
	httpClient *http.Client

	daneEnabled   bool
	mtaStsEnabled bool

	mu    sync.RWMutex
	cache map[string]cachedPolicy
	now   func() time.Time
}

// TlsaLookup returns the DANE associations published for a host, and whether
// the answer was DNSSEC-validated. An unvalidated answer must never be used:
// unsigned TLSA records are trivially forged (PLAN.md section 5.1).
type TlsaLookup interface {
	LookupTLSA(ctx context.Context, host string, port int) (records []valueobject.TLSARecord, authenticated bool, err error)
}

type cachedPolicy struct {
	policy    entity.TLSPolicy
	expiresAt time.Time
	// absent records that the destination publishes no MTA-STS policy, so
	// repeated sends do not re-probe it on every message.
	absent bool
}

type Option func(*Resolver)

// WithDane turns DANE resolution on. PLAN.md section 5.1 keeps it off unless
// the deployment manages its own certificates, because a TLSA record that
// stops matching after an ACME renewal blackholes mail permanently.
func WithDane(enabled bool, lookup TlsaLookup) Option {
	return func(r *Resolver) {
		r.daneEnabled = enabled
		r.tlsa = lookup
	}
}

func WithMtaSts(enabled bool) Option {
	return func(r *Resolver) { r.mtaStsEnabled = enabled }
}

func WithHTTPClient(client *http.Client) Option {
	return func(r *Resolver) { r.httpClient = client }
}

func WithClock(now func() time.Time) Option {
	return func(r *Resolver) { r.now = now }
}

func NewResolver(options ...Option) *Resolver {
	r := &Resolver{
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		mtaStsEnabled: true,
		cache:         map[string]cachedPolicy{},
		now:           time.Now,
	}
	for _, option := range options {
		option(r)
	}
	return r
}

func (r *Resolver) Resolve(ctx context.Context, destinationDomain string, mxHost string) (entity.TLSPolicy, error) {
	// DANE first: it is authenticated by DNSSEC and outranks MTA-STS
	// (RFC 8461 section 2).
	if r.daneEnabled && r.tlsa != nil {
		records, authenticated, err := r.tlsa.LookupTLSA(ctx, mxHost, 25)
		if err != nil {
			return entity.TLSPolicy{}, fmt.Errorf("lookup TLSA for %s: %w", mxHost, err)
		}
		if len(records) > 0 {
			if !authenticated {
				// Unsigned TLSA records carry no security value and acting on
				// them would let an attacker choose our policy.
				return entity.TLSPolicy{}, fmt.Errorf("TLSA records for %s are not DNSSEC-validated", mxHost)
			}
			return entity.NewDanePolicy(records), nil
		}
	}

	if !r.mtaStsEnabled {
		return entity.NewTLSPolicy(false, false), nil
	}

	policy, found, err := r.mtaStsPolicy(ctx, destinationDomain)
	if err != nil {
		return entity.TLSPolicy{}, err
	}
	if !found {
		return entity.NewTLSPolicy(false, false), nil
	}
	return policy, nil
}

func (r *Resolver) mtaStsPolicy(ctx context.Context, domain string) (entity.TLSPolicy, bool, error) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))

	if cached, ok := r.cached(domain); ok {
		return cached.policy, !cached.absent, nil
	}

	policy, maxAge, found, err := r.fetchPolicy(ctx, domain)
	if err != nil {
		return entity.TLSPolicy{}, false, err
	}

	// Cache the negative answer too, but only briefly: a domain that starts
	// publishing a policy should be picked up without a long delay.
	ttl := time.Duration(maxAge) * time.Second
	if !found {
		ttl = time.Hour
	}
	r.store(domain, cachedPolicy{policy: policy, expiresAt: r.now().Add(ttl), absent: !found})

	return policy, found, nil
}

// fetchPolicy retrieves and parses https://mta-sts.<domain>/.well-known/mta-sts.txt
// (RFC 8461 section 3.3).
func (r *Resolver) fetchPolicy(ctx context.Context, domain string) (policy entity.TLSPolicy, maxAge int, found bool, err error) {
	url := fmt.Sprintf("https://mta-sts.%s/.well-known/mta-sts.txt", domain)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return entity.TLSPolicy{}, 0, false, err
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		// A destination that publishes no policy host is simply not an
		// MTA-STS destination; that is not an error for us.
		return entity.TLSPolicy{}, 0, false, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return entity.TLSPolicy{}, 0, false, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, policyFetchLimit))
	if err != nil {
		return entity.TLSPolicy{}, 0, false, nil
	}

	mode, patterns, maxAge := parsePolicy(string(body))
	if mode == "" || len(patterns) == 0 {
		return entity.TLSPolicy{}, 0, false, nil
	}
	if mode == string(valueobject.MtaStsModeNone) {
		// An explicit "none" is the destination withdrawing its policy.
		return entity.TLSPolicy{}, 0, false, nil
	}

	enforce := mode == string(valueobject.MtaStsModeEnforce)
	return entity.NewMtaStsPolicy(patterns, enforce), maxAge, true, nil
}

// parsePolicy reads the key/value policy format of RFC 8461 section 3.2.
func parsePolicy(body string) (mode string, mxPatterns []string, maxAge int) {
	for _, line := range strings.Split(body, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch key {
		case "mode":
			mode = strings.ToLower(value)
		case "mx":
			mxPatterns = append(mxPatterns, value)
		case "max_age":
			maxAge, _ = strconv.Atoi(value)
		}
	}

	// RFC 8461 section 3.2 caps max_age at 31557600 seconds; a missing or
	// absurd value falls back to a day so a stale policy cannot be pinned
	// forever by a malformed file.
	if maxAge <= 0 || maxAge > 31557600 {
		maxAge = 86400
	}
	return mode, mxPatterns, maxAge
}

func (r *Resolver) cached(domain string) (cachedPolicy, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.cache[domain]
	if !ok || r.now().After(entry.expiresAt) {
		return cachedPolicy{}, false
	}
	return entry, true
}

func (r *Resolver) store(domain string, entry cachedPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[domain] = entry
}

// Ensure port interface compliance
var _ port.TLSPolicyResolver = (*Resolver)(nil)
