package httppresentation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"lambdamail/protocols/internal/domain/bimi"
)

// BimiStore holds the mark a domain publishes.
type BimiStore interface {
	Save(ctx context.Context, domain string, svg []byte, vmcURL string) (string, error)
	Load(ctx context.Context, domain string) (*BimiLogo, error)
	Delete(ctx context.Context, domain string) error
}

// BimiLogo is a stored mark as this layer sees it.
type BimiLogo struct {
	SVG    []byte
	ETag   string
	VmcURL string
}

type bimiAPI struct {
	store    BimiStore
	sessions *WebSessionVerifier
	// domainOf maps the host a request arrived on to the mail domain whose
	// logo should answer, so one deployment can serve several.
	defaultDomain string
}

// SetBimiAPI wires the BIMI logo store.
func (r *Router) SetBimiAPI(store BimiStore, sessionSecret, defaultDomain string) {
	if store == nil {
		return
	}
	r.bimi = &bimiAPI{store: store, defaultDomain: defaultDomain}
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
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required")
		return
	}

	domain := r.bimi.domainFor(req)
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

// handleBimiAdmin uploads, inspects or removes a domain's mark.
func (r *Router) handleBimiAdmin(w http.ResponseWriter, req *http.Request) {
	if r.bimi == nil || r.bimi.sessions == nil {
		writeError(w, http.StatusServiceUnavailable, "BIMI_DISABLED",
			"The BIMI panel needs JWT_SECRET and a database")
		return
	}

	token := bearerOrCookie(req, "lm_admin_session")
	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return
	}
	if _, err := r.bimi.sessions.RequireSurface(token, "admin"); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return
	}

	domain := strings.TrimSpace(req.URL.Query().Get("domain"))
	if domain == "" {
		writeError(w, http.StatusBadRequest, "DOMAIN_REQUIRED", "A domain is required")
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
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET, PUT or DELETE")
	}
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
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": true,
		"etag":       logo.ETag,
		"vmc_url":    logo.VmcURL,
		"bytes":      len(logo.SVG),
		"svg":        string(logo.SVG),
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "etag": etag})
}

// BimiLogoURL is where a domain's mark is served from.
func BimiLogoURL(mailHost string) string {
	return fmt.Sprintf("https://%s/.well-known/bimi/default.svg", mailHost)
}
