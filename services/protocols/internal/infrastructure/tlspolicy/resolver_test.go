package tlspolicy

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/domain/valueobject"
)

// policyServer stands in for https://mta-sts.<domain>/.well-known/mta-sts.txt.
// The resolver builds that URL from the domain, so the test rewrites the
// request through a custom transport instead of resolving real DNS.
func policyServer(t *testing.T, body string, status int) (*http.Client, *int) {
	t.Helper()

	hits := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/.well-known/mta-sts.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	client := server.Client()
	base := client.Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client.Transport = rewriteTransport{target: strings.TrimPrefix(server.URL, "https://"), inner: base}

	return client, &hits
}

type rewriteTransport struct {
	target string
	inner  http.RoundTripper
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Host = rt.target
	return rt.inner.RoundTrip(clone)
}

type stubTlsaLookup struct {
	records       []valueobject.TLSARecord
	authenticated bool
	err           error
}

func (s stubTlsaLookup) LookupTLSA(_ context.Context, _ string, _ int) ([]valueobject.TLSARecord, bool, error) {
	return s.records, s.authenticated, s.err
}

func TestResolver_MtaStsEnforcePolicyIsParsed(t *testing.T) {
	client, _ := policyServer(t, "version: STSv1\r\nmode: enforce\r\nmx: mx1.example.test\r\nmx: *.backup.test\r\nmax_age: 604800\r\n", http.StatusOK)

	policy, err := NewResolver(WithHTTPClient(client)).
		Resolve(context.Background(), "example.test", "mx1.example.test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if policy.Effective() != entity.TLSModeMtaSts {
		t.Errorf("Effective = %q, want mta-sts", policy.Effective())
	}
	if !policy.RequiresValidation() {
		t.Error("an enforce policy must require validation")
	}
	if !policy.CoversHost("mx1.example.test") || !policy.CoversHost("a.backup.test") {
		t.Error("the mx patterns were not carried into the policy")
	}
	if policy.CoversHost("mx.evil.test") {
		t.Error("an unlisted host must not be covered")
	}
}

func TestResolver_MtaStsTestingModeDoesNotEnforce(t *testing.T) {
	client, _ := policyServer(t, "version: STSv1\nmode: testing\nmx: mx1.example.test\nmax_age: 86400\n", http.StatusOK)

	policy, err := NewResolver(WithHTTPClient(client)).
		Resolve(context.Background(), "example.test", "mx1.example.test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if policy.RequiresValidation() {
		t.Error("mode: testing must not enforce")
	}
}

// A destination with no policy endpoint is opportunistic, which is not an
// error: most of the internet is in this state.
func TestResolver_MissingPolicyIsOpportunistic(t *testing.T) {
	client, _ := policyServer(t, "", http.StatusNotFound)

	policy, err := NewResolver(WithHTTPClient(client)).
		Resolve(context.Background(), "example.test", "mx1.example.test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if policy.Effective() != entity.TLSModeOpportunistic {
		t.Errorf("Effective = %q, want opportunistic", policy.Effective())
	}
}

// RFC 8461 section 3.2 caches the policy for max_age; re-fetching on every
// message would hammer the destination and slow every send.
func TestResolver_CachesPolicyForMaxAge(t *testing.T) {
	client, hits := policyServer(t, "version: STSv1\nmode: enforce\nmx: mx1.example.test\nmax_age: 604800\n", http.StatusOK)
	resolver := NewResolver(WithHTTPClient(client))
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := resolver.Resolve(ctx, "example.test", "mx1.example.test"); err != nil {
			t.Fatalf("Resolve %d: %v", i, err)
		}
	}

	if *hits != 1 {
		t.Errorf("fetched the policy %d times, want 1", *hits)
	}
}

func TestResolver_RefetchesAfterCacheExpiry(t *testing.T) {
	client, hits := policyServer(t, "version: STSv1\nmode: enforce\nmx: mx1.example.test\nmax_age: 60\n", http.StatusOK)

	now := time.Now()
	resolver := NewResolver(WithHTTPClient(client), WithClock(func() time.Time { return now }))
	ctx := context.Background()

	resolver.Resolve(ctx, "example.test", "mx1.example.test")
	now = now.Add(2 * time.Minute)
	resolver.Resolve(ctx, "example.test", "mx1.example.test")

	if *hits != 2 {
		t.Errorf("fetched the policy %d times, want 2 (cache expired between calls)", *hits)
	}
}

// An explicit "mode: none" is the destination withdrawing its policy.
func TestResolver_ModeNoneIsOpportunistic(t *testing.T) {
	client, _ := policyServer(t, "version: STSv1\nmode: none\nmx: mx1.example.test\nmax_age: 86400\n", http.StatusOK)

	policy, _ := NewResolver(WithHTTPClient(client)).
		Resolve(context.Background(), "example.test", "mx1.example.test")
	if policy.Effective() != entity.TLSModeOpportunistic {
		t.Errorf("Effective = %q, want opportunistic", policy.Effective())
	}
}

// DANE outranks MTA-STS, so a destination with TLSA records never reaches the
// HTTPS fetch (RFC 8461 section 2).
func TestResolver_DaneWinsAndSkipsMtaStsFetch(t *testing.T) {
	client, hits := policyServer(t, "version: STSv1\nmode: enforce\nmx: mx1.example.test\nmax_age: 86400\n", http.StatusOK)
	record, _ := valueobject.NewTLSARecord(3, 1, 1, strings.Repeat("ab", 32))

	policy, err := NewResolver(
		WithHTTPClient(client),
		WithDane(true, stubTlsaLookup{records: []valueobject.TLSARecord{record}, authenticated: true}),
	).Resolve(context.Background(), "example.test", "mx1.example.test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if policy.Effective() != entity.TLSModeDane {
		t.Errorf("Effective = %q, want dane", policy.Effective())
	}
	if *hits != 0 {
		t.Error("the MTA-STS policy was fetched even though DANE applies")
	}
}

// Acting on unsigned TLSA records would let anyone who can spoof DNS choose
// our transport policy (PLAN.md section 5.1).
func TestResolver_RefusesUnauthenticatedTlsaRecords(t *testing.T) {
	record, _ := valueobject.NewTLSARecord(3, 1, 1, strings.Repeat("ab", 32))

	_, err := NewResolver(
		WithDane(true, stubTlsaLookup{records: []valueobject.TLSARecord{record}, authenticated: false}),
	).Resolve(context.Background(), "example.test", "mx1.example.test")

	if err == nil {
		t.Fatal("expected unsigned TLSA records to be refused")
	}
	if !strings.Contains(err.Error(), "DNSSEC") {
		t.Errorf("error should explain the DNSSEC requirement: %v", err)
	}
}

// A destination with DANE disabled must fall through to MTA-STS.
func TestResolver_DaneDisabledFallsBackToMtaSts(t *testing.T) {
	client, _ := policyServer(t, "version: STSv1\nmode: enforce\nmx: mx1.example.test\nmax_age: 86400\n", http.StatusOK)

	policy, err := NewResolver(WithHTTPClient(client), WithDane(false, stubTlsaLookup{})).
		Resolve(context.Background(), "example.test", "mx1.example.test")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if policy.Effective() != entity.TLSModeMtaSts {
		t.Errorf("Effective = %q, want mta-sts", policy.Effective())
	}
}

func TestParsePolicy_ClampsAbsurdMaxAge(t *testing.T) {
	_, _, maxAge := parsePolicy("mode: enforce\nmx: a.test\nmax_age: 999999999999\n")
	if maxAge != 86400 {
		t.Errorf("maxAge = %d, want the 86400 fallback", maxAge)
	}

	_, _, missing := parsePolicy("mode: enforce\nmx: a.test\n")
	if missing != 86400 {
		t.Errorf("maxAge = %d, want the 86400 fallback", missing)
	}
}
