package httppresentation

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/domain/valueobject"
)

type Router struct {
	mux           *http.ServeMux
	reportUseCase *appusecase.IngestReportsUseCase
	pingFunc      func() error
}

func NewRouter(reportUseCase *appusecase.IngestReportsUseCase, pingFunc func() error) *Router {
	r := &Router{
		mux:           http.NewServeMux(),
		reportUseCase: reportUseCase,
		pingFunc:      pingFunc,
	}
	r.registerRoutes()
	return r
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
	r.mux.HandleFunc("/api/v1/reports/dmarc", r.handleDmarcIngest)
	r.mux.HandleFunc("/api/v1/reports/tlsrpt", r.handleTlsRptIngest)
}

func (r *Router) handleHealth(w http.ResponseWriter, _ *http.Request) {
	if r.pingFunc != nil {
		if err := r.pingFunc(); err != nil {
			http.Error(w, fmt.Sprintf("unhealthy: %v", err), http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK\n"))
}

func (r *Router) handleMtaSts(w http.ResponseWriter, req *http.Request) {
	host := extractHost(req.Host)
	domainName := strings.TrimPrefix(host, "mta-sts.")

	policy := valueobject.NewMtaStsPolicy(valueobject.MtaStsModeEnforce, []string{fmt.Sprintf("mail.%s", domainName)}, 604800)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(policy.Format()))
}

func (r *Router) handleSecurityTxt(w http.ResponseWriter, req *http.Request) {
	host := extractHost(req.Host)
	expires := time.Now().AddDate(1, 0, 0).Format(time.RFC3339)

	content := fmt.Sprintf("Contact: mailto:security@%s\r\nExpires: %s\r\nPreferred-Languages: en, pt-BR, es\r\n", host, expires)

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

func extractHost(hostHeader string) string {
	if h, _, err := strings.Cut(hostHeader, ":"); err && h != "" {
		return h
	}
	if hostHeader == "" {
		return "example.test"
	}
	return hostHeader
}
