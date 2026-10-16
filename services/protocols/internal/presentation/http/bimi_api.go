package httppresentation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"lambdamail/protocols/internal/domain/bimi"
	"lambdamail/protocols/internal/domain/entity"
)

// BimiStore holds the mark a domain publishes, and the certificate that
// vouches for it.
type BimiStore interface {
	Save(ctx context.Context, domain string, svg []byte, vmcURL string) (string, error)
	Load(ctx context.Context, domain string) (*BimiLogo, error)
	Delete(ctx context.Context, domain string) error
	// SaveCertificate reports false when the domain has no logo to attach it to.
	SaveCertificate(ctx context.Context, domain string, pem []byte) (bool, error)
	DeleteCertificate(ctx context.Context, domain string) error
}

// BimiLogo is a stored mark as this layer sees it.
type BimiLogo struct {
	SVG    []byte
	ETag   string
	VmcURL string
	// VmcPEM is the certificate chain held here, if the operator uploaded one.
	VmcPEM []byte
}

type bimiAPI struct {
	store    BimiStore
	sessions *WebSessionVerifier
	// domainOf maps the host a request arrived on to the mail domain whose
	// logo should answer, so one deployment can serve several.
	defaultDomain string
	// mailHost is where the logo and the certificate are served from: the host
	// the DNS record names.
	mailHost string
	now      func() time.Time
	dns      bimi.Resolver
	fetch    bimi.Fetcher
	// publish is told when a domain's logo or certificate changed, so the DNS
	// record can follow. Nil when there is no DNS provider to publish to.
	publish func(domain string)
}

// SetBimiPublisher wires what happens after a logo or certificate changes. It
// may be called before or after SetBimiAPI: it was once called before, found no
// BIMI API to attach to, and was dropped without a word, so saving a logo never
// published anything.
func (r *Router) SetBimiPublisher(publish func(domain string)) {
	r.bimiPublish = publish
	if r.bimi != nil {
		r.bimi.publish = publish
	}
}

// changed tells the publisher, in the background: a change is not slowed
// down by a round trip to the DNS provider, and its outcome is the
// reconciliation result the console shows, not part of the save.
func (a *bimiAPI) changed(domain string) {
	if a.publish != nil {
		go a.publish(domain)
	}
}

// SetBimiAPI wires the BIMI logo store.
func (r *Router) SetBimiAPI(store BimiStore, sessionSecret, defaultDomain, mailHost string) {
	if store == nil {
		return
	}
	r.bimi = &bimiAPI{
		store:         store,
		defaultDomain: defaultDomain,
		mailHost:      mailHost,
		now:           time.Now,
		dns:           netResolver{},
		fetch:         newBimiFetcher(),
		publish:       r.bimiPublish,
	}
	if sessionSecret != "" {
		r.bimi.sessions = NewWebSessionVerifier(sessionSecret)
	}
}

// handlePublicLogo serves the mark itself.
//
// Deliberately open: this is fetched by receiving mail providers evaluating a
// message, which hold no session here and never will. It is also the only
// thing in this service that is meant to be read by strangers, so it answers
// nothing but the stored bytes.
func (r *Router) handleBimiLogo(w http.ResponseWriter, req *http.Request) {
	if r.bimi == nil {
		http.NotFound(w, req)
		return
	}
	r.serveBimiLogo(w, req, r.bimi.domainFor(req))
}

func (r *Router) serveBimiLogo(w http.ResponseWriter, req *http.Request, domain string) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required")
		return
	}

	logo, err := r.bimi.store.Load(req.Context(), domain)
	if err != nil || logo == nil {
		http.NotFound(w, req)
		return
	}

	if req.Header.Get("If-None-Match") == `"`+logo.ETag+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("ETag", `"`+logo.ETag+`"`)
	// Public and long: receivers fetch this for every message they evaluate,
	// and the mark changes about as often as a company rebrands.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The document is served from the same origin as the console, and an SVG
	// is a document a browser will happily execute. Nothing in a validated
	// Tiny PS mark is active, but the header costs nothing and covers the case
	// where validation is one day loosened.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if req.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(logo.SVG)
}

func (a *bimiAPI) domainFor(req *http.Request) string {
	host := extractHost(req.Host)
	// mail.example.test serves example.test's mark; anything else falls back
	// to the domain this deployment was configured with.
	if trimmed := strings.TrimPrefix(host, "mail."); trimmed != host && trimmed != "" {
		return trimmed
	}
	return a.defaultDomain
}

// requireBimiAdmin answers the admin session and domain checks shared by every
// BIMI console route. It writes the refusal itself and returns false.
func (r *Router) requireBimiAdmin(w http.ResponseWriter, req *http.Request) (string, bool) {
	if r.bimi == nil || r.bimi.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "BIMI_DISABLED",
			"The BIMI panel needs JWT_SECRET and a database")
		return "", false
	}

	token := bearerOrCookie(req, "lm_admin_session")
	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return "", false
	}
	if _, err := r.bimi.sessions.RequireSurface(token, "admin"); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return "", false
	}

	domain := strings.TrimSpace(req.URL.Query().Get("domain"))
	if domain == "" {
		writeError(w, http.StatusBadRequest, "DOMAIN_REQUIRED", "A domain is required")
		return "", false
	}
	return domain, true
}

// handleBimiAdmin uploads, inspects or removes a domain's mark.
func (r *Router) handleBimiAdmin(w http.ResponseWriter, req *http.Request) {
	domain, ok := r.requireBimiAdmin(w, req)
	if !ok {
		return
	}

	switch req.Method {
	case http.MethodGet:
		r.bimi.get(w, req, domain)
	case http.MethodPut:
		r.bimi.put(w, req, domain)
	case http.MethodDelete:
		if err := r.bimi.store.Delete(req.Context(), domain); err != nil {
			log.Printf("bimi: could not delete the logo for %s: %v", domain, err)
			writeError(w, http.StatusInternalServerError, "DELETE_FAILED", "Could not remove the logo")
			return
		}
		r.bimi.changed(domain)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET, PUT or DELETE")
	}
}

// handleBimiCertificate serves the certificate chain, like the logo: fetched
// by receiving providers that hold no session.
func (r *Router) handleBimiCertificate(w http.ResponseWriter, req *http.Request) {
	if r.bimi == nil {
		http.NotFound(w, req)
		return
	}
	r.serveBimiCertificate(w, req, r.bimi.domainFor(req))
}

func (r *Router) serveBimiCertificate(w http.ResponseWriter, req *http.Request, domain string) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required")
		return
	}

	logo, err := r.bimi.store.Load(req.Context(), domain)
	if err != nil || logo == nil || len(logo.VmcPEM) == 0 {
		http.NotFound(w, req)
		return
	}

	sum := sha256.Sum256(logo.VmcPEM)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	if req.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("ETag", etag)
	// Shorter than the logo: a renewed certificate should reach receivers
	// within the hour, not the day.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if req.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(logo.VmcPEM)
}

// handleBimiCertificateAdmin uploads or removes the certificate chain.
func (r *Router) handleBimiCertificateAdmin(w http.ResponseWriter, req *http.Request) {
	domain, ok := r.requireBimiAdmin(w, req)
	if !ok {
		return
	}

	switch req.Method {
	case http.MethodPut:
		r.bimi.putCertificate(w, req, domain)
	case http.MethodDelete:
		if err := r.bimi.store.DeleteCertificate(req.Context(), domain); err != nil {
			log.Printf("bimi: could not delete the certificate for %s: %v", domain, err)
			writeError(w, http.StatusInternalServerError, "DELETE_FAILED", "Could not remove the certificate")
			return
		}
		r.bimi.changed(domain)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "PUT or DELETE")
	}
}

// handleBimiCheck looks at everything a receiver would look at, and says which
// part is wrong.
func (r *Router) handleBimiCheck(w http.ResponseWriter, req *http.Request) {
	domain, ok := r.requireBimiAdmin(w, req)
	if !ok {
		return
	}
	if req.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required")
		return
	}

	logo, err := r.bimi.store.Load(req.Context(), domain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LOAD_FAILED", "Could not read the logo")
		return
	}
	if logo == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "checks": []any{}})
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 25*time.Second)
	defer cancel()

	// What the record should say is decided once (bimi.Publish), exactly as the
	// sync writes it, so "the record is wrong" means it differs from what the
	// reconciler would publish.
	publication := bimi.Publish(logo.SVG, logo.VmcPEM, logo.VmcURL, domain,
		BimiLogoURL(r.bimi.mailHost, domain), BimiVmcURL(r.bimi.mailHost, domain), r.bimi.now())
	in := bimi.Inputs{
		Domain:                 domain,
		LogoURL:                publication.LogoURL,
		CertificateURL:         publication.CertificateURL,
		SVG:                    logo.SVG,
		CertificatePEM:         logo.VmcPEM,
		ExternalCertificateURL: logo.VmcURL,
		Withheld:               publication.Withheld,
		Now:                    r.bimi.now(),
	}

	results := bimi.RunChecks(ctx, in, r.bimi.dns, r.bimi.fetch)
	checks := make([]map[string]string, 0, len(results))
	ready := true
	for _, c := range results {
		checks = append(checks, map[string]string{"id": c.ID, "status": string(c.Status), "detail": c.Detail})
		// A missing certificate is a warning, not a fault: some receivers show the logo without one
		if c.Status == bimi.StatusFail {
			ready = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "ready": ready, "checks": checks})
}

func (a *bimiAPI) get(w http.ResponseWriter, req *http.Request, domain string) {
	logo, err := a.store.Load(req.Context(), domain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LOAD_FAILED", "Could not read the logo")
		return
	}
	if logo == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false})
		return
	}
	publication := bimi.Publish(logo.SVG, logo.VmcPEM, logo.VmcURL, domain,
		BimiLogoURL(a.mailHost, domain), BimiVmcURL(a.mailHost, domain), a.now())
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": true,
		"etag":       logo.ETag,
		// The record exactly as the reconciler writes it, and the reason a held
		// certificate is left out of it, if it is
		"dns_record":           entity.BimiRecordValue(publication.LogoURL, publication.CertificateURL),
		"certificate_withheld": publication.Withheld,
		"dns_publishing":       a.publish != nil,
		"vmc_url":              logo.VmcURL,
		"bytes":                len(logo.SVG),
		"svg":                  string(logo.SVG),
		"logo_url":             BimiLogoURL(a.mailHost, domain),
		"certificate_url":      BimiVmcURL(a.mailHost, domain),
		"certificate":          a.describeCertificate(domain, logo),
	})
}

// describeCertificate reports the held certificate as it stands against the
// logo now being served. Read from the file each time, so it cannot drift from
// what receivers get.
func (a *bimiAPI) describeCertificate(domain string, logo *BimiLogo) map[string]any {
	if len(logo.VmcPEM) == 0 {
		return map[string]any{"present": false}
	}
	cert, violations := bimi.InspectCertificate(logo.VmcPEM, domain, logo.SVG, a.now())
	reasons := make([]string, 0, len(violations))
	for _, v := range violations {
		reasons = append(reasons, v.String())
	}
	out := map[string]any{"present": true, "valid": len(violations) == 0, "violations": reasons}
	if cert != nil {
		out["subject"] = cert.Subject
		out["organization"] = cert.Organization
		out["issuer"] = cert.Issuer
		out["not_before"] = cert.NotBefore.UTC().Format(time.RFC3339)
		out["not_after"] = cert.NotAfter.UTC().Format(time.RFC3339)
		out["dns_names"] = cert.DNSNames
		out["mark_type"] = cert.MarkType
		out["kind"] = string(cert.Kind)
		out["chain_length"] = cert.ChainLength
		out["matches_logo"] = cert.MatchesLogo
		out["warnings"] = cert.Warnings
	}
	return out
}

// putCertificate stores a certificate chain after checking it against the
// logo and the domain. A certificate that does not match is refused rather
// than stored: publishing one makes receivers ignore BIMI without a word.
func (a *bimiAPI) putCertificate(w http.ResponseWriter, req *http.Request, domain string) {
	body, err := io.ReadAll(io.LimitReader(req.Body, bimi.MaxCertificateBytes*2))
	if err != nil {
		writeError(w, http.StatusBadRequest, "READ_FAILED", "Could not read the upload")
		return
	}
	var payload struct {
		PEM string `json:"pem"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Expected a JSON body with a pem field")
		return
	}

	logo, err := a.store.Load(req.Context(), domain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LOAD_FAILED", "Could not read the logo")
		return
	}
	if logo == nil {
		writeError(w, http.StatusConflict, "NO_LOGO", "Save the logo first: a certificate only means something next to the logo it was issued for")
		return
	}

	pem := []byte(strings.TrimSpace(payload.PEM) + "\n")
	_, violations := bimi.InspectCertificate(pem, domain, logo.SVG, a.now())
	if len(violations) > 0 {
		reasons := make([]string, 0, len(violations))
		for _, v := range violations {
			reasons = append(reasons, v.String())
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":      "INVALID_CERTIFICATE",
			"message":    "The certificate cannot be used for this domain and logo",
			"violations": reasons,
		})
		return
	}

	saved, err := a.store.SaveCertificate(req.Context(), domain, pem)
	if err != nil {
		log.Printf("bimi: could not save the certificate for %s: %v", domain, err)
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", "Could not store the certificate")
		return
	}
	if !saved {
		writeError(w, http.StatusConflict, "NO_LOGO", "Save the logo first")
		return
	}
	a.changed(domain)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"dns_publishing": a.publish != nil,
		"certificate":    a.describeCertificate(domain, &BimiLogo{SVG: logo.SVG, VmcPEM: pem}),
	})
}

func (a *bimiAPI) put(w http.ResponseWriter, req *http.Request, domain string) {
	// A shade over the specification's ceiling, so an oversized upload is
	// refused by the validator with a reason rather than truncated here.
	body, err := io.ReadAll(io.LimitReader(req.Body, bimi.MaxLogoBytes*2))
	if err != nil {
		writeError(w, http.StatusBadRequest, "READ_FAILED", "Could not read the upload")
		return
	}

	var payload struct {
		SVG    string `json:"svg"`
		VmcURL string `json:"vmc_url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Expected a JSON body with an svg field")
		return
	}

	// Every reason at once. Fixing one rule at a time against a receiver that
	// silently shows nothing is a miserable way to learn this format.
	if violations := bimi.Validate([]byte(payload.SVG)); len(violations) > 0 {
		reasons := make([]string, 0, len(violations))
		for _, v := range violations {
			reasons = append(reasons, v.String())
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":      "INVALID_LOGO",
			"message":    "The logo is not SVG Tiny Portable/Secure",
			"violations": reasons,
		})
		return
	}

	vmc := strings.TrimSpace(payload.VmcURL)
	if vmc != "" && !strings.HasPrefix(vmc, "https://") {
		// A certificate fetched over plain HTTP is not evidence of anything,
		// and every receiver refuses it.
		writeError(w, http.StatusBadRequest, "INSECURE_VMC", "The certificate URL must be https")
		return
	}

	etag, err := a.store.Save(req.Context(), domain, []byte(payload.SVG), vmc)
	if err != nil {
		log.Printf("bimi: could not save the logo for %s: %v", domain, err)
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", "Could not store the logo")
		return
	}
	// A certificate vouches for one exact SVG. Replacing the logo under a held
	// certificate breaks it, which receivers do not report; say so now.
	a.changed(domain)
	response := map[string]any{"ok": true, "etag": etag, "dns_publishing": a.publish != nil}
	if held, err := a.store.Load(req.Context(), domain); err == nil && held != nil && len(held.VmcPEM) > 0 {
		if cert, _ := bimi.InspectCertificate(held.VmcPEM, domain, held.SVG, a.now()); cert != nil && !cert.MatchesLogo {
			response["certificate_mismatch"] = true
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// domainName accepts what a mail domain can be, and nothing a path could use to
// reach anything else.
var domainName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// handleBimiPerDomain serves /.well-known/bimi/<domain>/default.svg and
// /.well-known/bimi/<domain>/vmc.pem.
//
// The legacy paths pick the domain from the host the request arrived on, which
// can name only the one domain whose mail host that is. The record a domain
// publishes names a URL on the deployment's mail host whatever the domain is,
// so every domain other than the primary one pointed at a logo that was not its
// own and got a 404.
func (r *Router) handleBimiPerDomain(w http.ResponseWriter, req *http.Request) {
	if r.bimi == nil {
		http.NotFound(w, req)
		return
	}
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/.well-known/bimi/"), "/")
	if len(parts) != 2 || !domainName.MatchString(strings.ToLower(parts[0])) {
		http.NotFound(w, req)
		return
	}
	domain := strings.ToLower(parts[0])
	switch parts[1] {
	case "default.svg":
		r.serveBimiLogo(w, req, domain)
	case "vmc.pem":
		r.serveBimiCertificate(w, req, domain)
	default:
		http.NotFound(w, req)
	}
}

// BimiLogoURL is where a domain's mark is served from.
func BimiLogoURL(mailHost, domain string) string {
	return fmt.Sprintf("https://%s/.well-known/bimi/%s/default.svg", mailHost, strings.ToLower(domain))
}

// BimiVmcURL is where the certificate held for a domain is served from.
func BimiVmcURL(mailHost, domain string) string {
	return fmt.Sprintf("https://%s/.well-known/bimi/%s/vmc.pem", mailHost, strings.ToLower(domain))
}
