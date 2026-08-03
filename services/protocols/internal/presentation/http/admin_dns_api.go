package httppresentation

import (
	"context"
	"net/http"
	"strings"

	"lambdamail/protocols/internal/domain/entity"
)

// DnsRecordVerifier answers whether one expected record is actually published.
// Implemented by the same public-resolver verifier the reconciler uses, so the
// console and the background sync agree on what "verified" means.
type DnsRecordVerifier interface {
	VerifyRecord(ctx context.Context, record entity.DnsRecord) (bool, string)
}

// DnsSpecSource builds the records a domain is expected to publish. It lives
// behind an interface because assembling the spec needs the DKIM public keys,
// which only the service holding the vault can read.
type DnsSpecSource interface {
	ExpectedRecords(ctx context.Context, domain string) ([]entity.DnsRecord, error)
}

type adminDnsAPI struct {
	spec     DnsSpecSource
	verifier DnsRecordVerifier
	sessions *WebSessionVerifier
}

// handleVerify checks every expected record against public resolvers and
// reports each one individually.
//
// This is the endpoint the admin console's reconcile button had no equivalent
// of. The auth service answered it by writing an audit row and re-reading the
// stored status, because it has no resolver - so the console could only ever
// repeat what the database already believed. Verification belongs here, with
// the resolver and the record spec.
func (a *adminDnsAPI) handleVerify(w http.ResponseWriter, r *http.Request) {
	token := bearerOrCookie(r, "lm_admin_session")
	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return
	}
	if _, err := a.sessions.RequireSurface(token, "admin"); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return
	}

	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		writeError(w, http.StatusBadRequest, "DOMAIN_REQUIRED", "A domain is required")
		return
	}

	expected, err := a.spec.ExpectedRecords(r.Context(), domain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "SPEC_FAILED", "Could not assemble the expected records")
		return
	}

	type result struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Expected string `json:"expected"`
		Verified bool   `json:"verified"`
		Detail   string `json:"detail"`
		Proxied  bool   `json:"proxied"`
	}

	results := make([]result, 0, len(expected))
	verified := 0
	for _, record := range expected {
		ok, detail := a.verifier.VerifyRecord(r.Context(), record)
		if ok {
			verified++
		}
		results = append(results, result{
			Type:     record.Type,
			Name:     record.Name,
			Expected: record.Value,
			Verified: ok,
			Detail:   detail,
			// Reported so the console can explain why a proxied record is
			// checked for presence only: behind a proxy the published answer
			// is the provider's address, never the value we asked for.
			Proxied: record.Proxied,
		})
	}

	status := "VERIFIED"
	switch {
	case len(results) == 0:
		status = "UNKNOWN"
	case verified == 0:
		status = "MISSING"
	case verified < len(results):
		status = "PARTIAL"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"domain":   domain,
		"status":   status,
		"verified": verified,
		"total":    len(results),
		"records":  results,
	})
}

// SetAdminDnsAPI wires per-record DNS verification. Without a session secret
// the route answers 503 rather than opening unauthenticated.
func (r *Router) SetAdminDnsAPI(spec DnsSpecSource, verifier DnsRecordVerifier, sessionSecret string) {
	if spec == nil || verifier == nil || sessionSecret == "" {
		return
	}
	r.dns = &adminDnsAPI{spec: spec, verifier: verifier, sessions: NewWebSessionVerifier(sessionSecret)}
}

func (r *Router) handleAdminDnsVerify(w http.ResponseWriter, req *http.Request) {
	if r.dns == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS_API_DISABLED",
			"DNS verification needs JWT_SECRET and a configured domain")
		return
	}
	r.dns.handleVerify(w, req)
}
