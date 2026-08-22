package httppresentation

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/domain/valueobject"
)

// mtaStsMaxAge is the policy lifetime advertised to senders. RFC 8461
// recommends a value in the order of weeks so that an attacker cannot strip
// the policy by making the endpoint unreachable.
const mtaStsMaxAge = 604800 // 7 days

type Router struct {
	mux           *http.ServeMux
	reportUseCase *appusecase.IngestReportsUseCase
	pingFunc      func() error
	mtaStsMode    valueobject.MtaStsMode
	// mail serves the webmail's message screens. It stays nil when no session
	// secret is configured, and the routes then answer 503 rather than
	// pretending to be open.
	mail *mailAPI
	// events pushes new mail to the browser. Like mail, it stays nil without a
	// session secret.
	events *EventHub
	// dkim manages signing keys. It lives here because the private key must be
	// sealed with this process's vault.
	dkim *adminDkimAPI
	// tls reports what the certificate watcher sees, rather than a constant.
	tls *adminTlsAPI
	// dns verifies the published records against public resolvers. It lives
	// here because this is the process with the resolver and the record spec.
	dns *adminDnsAPI
	// degradedFunc reports a condition that leaves the service running but
	// not fit for production, the clearest case being a self-signed
	// certificate standing in for one Traefik never issued
	// (PLAN.md section 8.4).
	degradedFunc func() error
}

func NewRouter(reportUseCase *appusecase.IngestReportsUseCase, pingFunc func() error) *Router {
	r := &Router{
		mux:           http.NewServeMux(),
		reportUseCase: reportUseCase,
		pingFunc:      pingFunc,
		// PLAN.md section 5 ramps MTA-STS from testing to enforce. Starting at
		// enforce means any TLS misconfiguration silently blackholes inbound
		// mail from every sender that honours the policy, with no way to tell
		// from this side - so the safe mode is the default and the operator
		// promotes it once TLS-RPT reports come back clean.
		mtaStsMode: valueobject.MtaStsModeTesting,
	}
	r.registerRoutes()
	return r
}

// SetMtaStsMode selects the mode advertised in the served policy. Callers
// should promote to enforce only after TLS-RPT confirms senders can negotiate
// TLS against the published MX.
func (r *Router) SetMtaStsMode(mode valueobject.MtaStsMode) {
	r.mtaStsMode = mode
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *Router) registerRoutes() {
	r.mux.HandleFunc("/health", r.handleHealth)
	r.mux.HandleFunc("/.well-known/mta-sts.txt", r.handleMtaSts)
	r.mux.HandleFunc("/.well-known/security.txt", r.handleSecurityTxt)
	r.mux.HandleFunc("/mail/config-v1.1.xml", r.handleThunderbirdAutoconfig)
	r.mux.HandleFunc("/.well-known/autoconfig/mail/config-v1.1.xml", r.handleThunderbirdAutoconfig)
	r.mux.HandleFunc("/autodiscover/autodiscover.xml", r.handleOutlookAutodiscover)
	r.mux.HandleFunc("/api/v1/events", r.handleEvents)
	r.mux.HandleFunc("/api/v1/admin/tls", r.handleAdminTls)
	r.mux.HandleFunc("/api/v1/admin/dns/verify", r.handleAdminDnsVerify)
	r.mux.HandleFunc("/api/v1/admin/dkim/keys", r.handleAdminDkimKeys)
	r.mux.HandleFunc("/api/v1/admin/dkim/rotate", r.handleAdminDkimRotate)
	r.mux.HandleFunc("/api/v1/mail/folders", r.handleMailFolders)
	r.mux.HandleFunc("/api/v1/mail/messages", r.handleMailMessages)
	r.mux.HandleFunc("/api/v1/mail/message/", r.handleMailMessage)
	r.mux.HandleFunc("/api/v1/mail/seen", r.handleMailSeen)
	r.mux.HandleFunc("/api/v1/mail/send", r.handleMailSend)
	r.mux.HandleFunc("/api/v1/mail/draft", r.handleMailDraft)
	r.mux.HandleFunc("/api/v1/mail/delete", r.handleMailDelete)
	r.mux.HandleFunc("/api/v1/mail/move", r.handleMailMove)
	r.mux.HandleFunc("/api/v1/reports/dmarc", r.handleDmarcIngest)
	r.mux.HandleFunc("/api/v1/reports/tlsrpt", r.handleTlsRptIngest)
}

// SetDegradedCheck registers the probe behind the "degraded" health state.
// Returning an error means the process is serving but something an operator
// must fix is wrong.
func (r *Router) SetDegradedCheck(check func() error) {
	r.degradedFunc = check
}

func (r *Router) handleHealth(w http.ResponseWriter, _ *http.Request) {
	if r.pingFunc != nil {
		if err := r.pingFunc(); err != nil {
			http.Error(w, fmt.Sprintf("unhealthy: %v", err), http.StatusServiceUnavailable)
			return
		}
	}

	// Degraded still answers 200: the container is alive and must not be
	// restarted in a loop over a certificate problem a restart cannot fix.
	// The distinction is carried in the body and the header so monitoring can
	// alert on it (PLAN.md section 8.4).
	if r.degradedFunc != nil {
		if err := r.degradedFunc(); err != nil {
			w.Header().Set("X-LambdaMail-Health", "degraded")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "DEGRADED: %v\n", err)
			return
		}
	}

	w.Header().Set("X-LambdaMail-Health", "ok")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK\n"))
}

func (r *Router) handleMtaSts(w http.ResponseWriter, req *http.Request) {
	host := extractHost(req.Host)
	domainName := strings.TrimPrefix(host, "mta-sts.")

	policy := valueobject.NewMtaStsPolicy(r.mtaStsMode, []string{fmt.Sprintf("mail.%s", domainName)}, mtaStsMaxAge)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// Kept in step with max_age so an HTTP cache cannot outlive the policy it
	// is caching.
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", mtaStsMaxAge))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(policy.Format()))
}

func (r *Router) handleSecurityTxt(w http.ResponseWriter, req *http.Request) {
	// The contact must be a real mailbox on the mail domain, not on whichever
	// service hostname the request happened to arrive at.
	domainName := baseDomain(extractHost(req.Host))
	expires := time.Now().AddDate(1, 0, 0).Format(time.RFC3339)

	content := fmt.Sprintf("Contact: mailto:security@%s\r\nExpires: %s\r\nPreferred-Languages: en, pt-BR, es\r\n", domainName, expires)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}

func (r *Router) handleThunderbirdAutoconfig(w http.ResponseWriter, req *http.Request) {
	host := extractHost(req.Host)
	domainName := strings.TrimPrefix(host, "autoconfig.")

	xml := valueobject.BuildThunderbirdAutoconfigXML(domainName, fmt.Sprintf("mail.%s", domainName))

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xml))
}

func (r *Router) handleOutlookAutodiscover(w http.ResponseWriter, req *http.Request) {
	host := extractHost(req.Host)
	domainName := strings.TrimPrefix(host, "autodiscover.")

	xml := valueobject.BuildOutlookAutodiscoverXML(domainName, fmt.Sprintf("mail.%s", domainName), fmt.Sprintf("user@%s", domainName))

	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xml))
}

func (r *Router) handleDmarcIngest(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Read body failed", http.StatusBadRequest)
		return
	}
	if r.reportUseCase == nil {
		http.Error(w, "Ingestion unconfigured", http.StatusInternalServerError)
		return
	}
	if _, err := r.reportUseCase.IngestDmarc(req.Context(), body); err != nil {
		http.Error(w, fmt.Sprintf("Parse error: %v", err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (r *Router) handleTlsRptIngest(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Read body failed", http.StatusBadRequest)
		return
	}
	if r.reportUseCase == nil {
		http.Error(w, "Ingestion unconfigured", http.StatusInternalServerError)
		return
	}
	if _, err := r.reportUseCase.IngestTlsRpt(req.Context(), body); err != nil {
		http.Error(w, fmt.Sprintf("Parse error: %v", err), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// extractHost strips the optional port from a Host header, handling the
// bracketed IPv6 literal form as well.
func extractHost(hostHeader string) string {
	if hostHeader == "" {
		return "example.test"
	}
	if host, _, err := net.SplitHostPort(hostHeader); err == nil && host != "" {
		return host
	}
	return strings.Trim(hostHeader, "[]")
}

// serviceHostPrefixes are the per-service names published under a mail domain
// (PLAN.md section 7.3); stripping one yields the mail domain itself.
var serviceHostPrefixes = []string{"mta-sts.", "autoconfig.", "autodiscover.", "mail."}

func baseDomain(host string) string {
	for _, prefix := range serviceHostPrefixes {
		if strings.HasPrefix(host, prefix) {
			return strings.TrimPrefix(host, prefix)
		}
	}
	return host
}

// SetMailAPI wires the webmail message API. Without it the mail routes exist
// but report that the feature is not configured, which is a clearer failure
// than a 404 that looks like a typo in the path.
func (r *Router) SetMailAPI(useCase *appusecase.WebmailUseCase, sessionSecret string) {
	if useCase == nil || sessionSecret == "" {
		return
	}
	r.mail = &mailAPI{useCase: useCase, sessions: NewWebSessionVerifier(sessionSecret)}
}

func (r *Router) mailReady(w http.ResponseWriter) bool {
	if r.mail == nil {
		writeError(w, http.StatusServiceUnavailable, "MAIL_API_DISABLED",
			"The mail API needs JWT_SECRET to verify webmail sessions")
		return false
	}
	return true
}

func (r *Router) handleMailFolders(w http.ResponseWriter, req *http.Request) {
	if r.mailReady(w) {
		r.mail.handleFolders(w, req)
	}
}

func (r *Router) handleMailMessages(w http.ResponseWriter, req *http.Request) {
	if r.mailReady(w) {
		r.mail.handleMessages(w, req)
	}
}

func (r *Router) handleMailMessage(w http.ResponseWriter, req *http.Request) {
	if r.mailReady(w) {
		r.mail.handleMessage(w, req)
	}
}

func (r *Router) handleMailSeen(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}
	if r.mailReady(w) {
		r.mail.handleSeen(w, req)
	}
}

func (r *Router) handleMailSend(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}
	if r.mailReady(w) {
		r.mail.handleSend(w, req)
	}
}

// handleMailDelete removes one message. There was no route for this at all,
// so nothing the webmail displayed could be deleted from it.
func (r *Router) handleMailDelete(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost && req.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST or DELETE required")
		return
	}
	if r.mailReady(w) {
		r.mail.handleDelete(w, req)
	}
}

// handleMailMove files one message into another folder.
func (r *Router) handleMailMove(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}
	if r.mailReady(w) {
		r.mail.handleMove(w, req)
	}
}

func (r *Router) handleMailDraft(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}
	if r.mailReady(w) {
		r.mail.handleSaveDraft(w, req)
	}
}
