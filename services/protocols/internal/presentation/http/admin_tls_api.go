package httppresentation

import (
	"net/http"
	"time"
)

// TlsStatusSource reports what the certificate watcher actually sees. The
// admin console used to receive fixed values - "VALID", 72 days - which told
// an operator the certificate was healthy no matter what the process held.
type TlsStatusSource interface {
	EarliestExpiry() (host string, expiry time.Time, ok bool)
	LastReload() time.Time
	LastChange() time.Time
	UnknownSNICount() int64
	HasCertificateFor(host string) bool
}

// TlsCertificateInspector is optional extra detail: who issued the
// certificate, and whether it is self-signed. A source that cannot say simply
// does not implement it.
type TlsCertificateInspector interface {
	CertificateSummary(host string) (issuer string, selfSigned bool, ok bool)
}

type adminTlsAPI struct {
	source     TlsStatusSource
	mailHost   string
	tlsMode    string
	sessions   *WebSessionVerifier
	pollPeriod time.Duration
}

func (a *adminTlsAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	token := bearerOrCookie(r, "lm_admin_session")
	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return
	}
	if _, err := a.sessions.RequireSurface(token, "admin"); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return
	}

	status := map[string]any{
		"tls_mode":          a.tlsMode,
		"mail_host":         a.mailHost,
		"has_certificate":   a.source.HasCertificateFor(a.mailHost),
		"unknown_sni_total": a.source.UnknownSNICount(),
		"last_reload":       nil,
		"last_change":       nil,
		// Null, not false. A source that reloads from disk reports a time and
		// gets a verdict; one that issues its own certificate has no watcher to
		// be unhealthy, and saying "unavailable" there is a false alarm.
		"watcher_healthy":     nil,
		"certificate_expires": nil,
		"expires_in_days":     nil,
		"state":               "UNKNOWN",
	}

	if last := a.source.LastReload(); !last.IsZero() {
		status["last_reload"] = last.UTC().Format(time.RFC3339)
		// Two missed polls is the smallest gap that is not a scheduling
		// hiccup, and it is the symptom of a watcher gone blind (risk R2).
		status["watcher_healthy"] = time.Since(last) < 3*a.pollPeriod
	}
	if change := a.source.LastChange(); !change.IsZero() {
		status["last_change"] = change.UTC().Format(time.RFC3339)
	}

	if host, expiry, ok := a.source.EarliestExpiry(); ok {
		remaining := time.Until(expiry)
		status["certificate_expires"] = expiry.UTC().Format(time.RFC3339)
		status["expires_in_days"] = int(remaining.Hours() / 24)
		status["soonest_expiring_host"] = host

		switch {
		case remaining <= 0:
			status["state"] = "EXPIRED"
		case remaining < 24*time.Hour:
			status["state"] = "CRITICAL"
		case remaining < 7*24*time.Hour:
			status["state"] = "WARNING"
		default:
			status["state"] = "OK"
		}
	}

	// Who signed it, and whether anything would trust it. A self-signed
	// certificate on 25 and 993 is refused by every client that verifies, so
	// it outranks an expiry that is comfortably far away.
	if inspector, ok := a.source.(TlsCertificateInspector); ok {
		if issuer, selfSigned, found := inspector.CertificateSummary(a.mailHost); found {
			status["issuer"] = issuer
			status["self_signed"] = selfSigned
			if selfSigned {
				status["state"] = "SELF_SIGNED"
			}
		}
	}

	if !status["has_certificate"].(bool) {
		status["state"] = "NO_CERTIFICATE"
	}

	writeJSON(w, http.StatusOK, status)
}

// SetAdminTlsAPI wires the TLS panel to the live certificate source.
func (r *Router) SetAdminTlsAPI(source TlsStatusSource, mailHost, tlsMode, sessionSecret string, pollPeriod time.Duration) {
	if source == nil || sessionSecret == "" {
		return
	}
	if pollPeriod <= 0 {
		pollPeriod = time.Minute
	}
	r.tls = &adminTlsAPI{
		source: source, mailHost: mailHost, tlsMode: tlsMode,
		sessions: NewWebSessionVerifier(sessionSecret), pollPeriod: pollPeriod,
	}
}

func (r *Router) handleAdminTls(w http.ResponseWriter, req *http.Request) {
	if r.tls == nil {
		writeError(w, http.StatusServiceUnavailable, "TLS_API_DISABLED",
			"The TLS panel needs a certificate watcher and JWT_SECRET")
		return
	}
	r.tls.handleStatus(w, req)
}
