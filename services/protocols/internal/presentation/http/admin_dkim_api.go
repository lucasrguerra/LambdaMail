package httppresentation

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"lambdamail/protocols/internal/infrastructure/postgres"
)

// dkimOverlap is how long the previous key stays resolvable after a rotation.
// Mail already in flight was signed with it and receivers may still be
// fetching the selector, so removing it at once would fail DKIM for messages
// that were valid when they were sent (PLAN.md section 5).
const dkimOverlap = 7 * 24 * time.Hour

// KeyGenerator produces a new DKIM key pair. It is injected so this package
// does not depend on the crypto implementation.
type KeyGenerator func(algorithm string) (privateKeyPEM []byte, publicKeyBase64 string, err error)

// adminDkimAPI serves DKIM key management.
//
// It lives in this service, not in the auth service, because the private key
// has to be sealed with the vault this process owns: the two services derive
// their keys differently, so a key written elsewhere could never be opened
// here - and a DKIM key that cannot be opened means unsigned outbound mail.
type adminDkimAPI struct {
	repo     *postgres.DkimRepository
	generate KeyGenerator
	sessions *WebSessionVerifier
}

// bearerOrCookie reads the session token from either transport.
func bearerOrCookie(r *http.Request, cookieName string) string {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	if cookie, err := r.Cookie(cookieName); err == nil {
		return cookie.Value
	}
	return ""
}

func (a *adminDkimAPI) authenticate(w http.ResponseWriter, r *http.Request) bool {
	token := bearerOrCookie(r, "lm_admin_session")

	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return false
	}
	if _, err := a.sessions.RequireSurface(token, "admin"); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Admin session required")
		return false
	}
	return true
}

func (a *adminDkimAPI) handleKeys(w http.ResponseWriter, r *http.Request) {
	if !a.authenticate(w, r) {
		return
	}

	domain := r.URL.Query().Get("domain")
	if domain == "" {
		writeError(w, http.StatusBadRequest, "DOMAIN_REQUIRED", "A domain is required")
		return
	}

	keys, err := a.repo.ListKeys(r.Context(), domain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not list keys")
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (a *adminDkimAPI) handleRotate(w http.ResponseWriter, r *http.Request) {
	if !a.authenticate(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}

	var body struct {
		Domain    string `json:"domain"`
		Selector  string `json:"selector"`
		Algorithm string `json:"algorithm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	if body.Algorithm != "rsa2048" && body.Algorithm != "ed25519" {
		writeError(w, http.StatusBadRequest, "INVALID_ALGORITHM", "Algorithm must be rsa2048 or ed25519")
		return
	}
	if body.Domain == "" {
		writeError(w, http.StatusBadRequest, "DOMAIN_REQUIRED", "A domain is required")
		return
	}
	// A selector becomes part of a DNS name, so it is restricted to what is
	// valid in one rather than trusted from the request.
	if !isValidSelector(body.Selector) {
		writeError(w, http.StatusBadRequest, "INVALID_SELECTOR",
			"A selector may contain letters, digits and hyphens, up to 63 characters")
		return
	}

	privateKeyPEM, publicKey, err := a.generate(body.Algorithm)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "KEYGEN_FAILED", "Could not generate a key")
		return
	}

	if err := a.repo.Rotate(r.Context(), body.Domain, body.Selector, body.Algorithm,
		privateKeyPEM, dkimPublicRecord(body.Algorithm, publicKey), dkimOverlap); err != nil {
		writeError(w, http.StatusInternalServerError, "ROTATE_FAILED", "Could not rotate the key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"domain":            body.Domain,
		"selector":          body.Selector,
		"algorithm":         body.Algorithm,
		"public_key_record": dkimPublicRecord(body.Algorithm, publicKey),
		"overlap_days":      int(dkimOverlap.Hours() / 24),
	})
}

// dkimPublicRecord renders the TXT value a resolver expects.
func dkimPublicRecord(algorithm, publicKeyBase64 string) string {
	keyType := "rsa"
	if algorithm == "ed25519" {
		keyType = "ed25519"
	}
	return "v=DKIM1; k=" + keyType + "; p=" + publicKeyBase64
}

func isValidSelector(selector string) bool {
	if selector == "" || len(selector) > 63 {
		return false
	}
	for _, r := range selector {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
		default:
			return false
		}
	}
	return true
}

// SetAdminDkimAPI wires DKIM management. Without a session secret the routes
// answer 503 rather than opening unauthenticated.
func (r *Router) SetAdminDkimAPI(repo *postgres.DkimRepository, generate KeyGenerator, sessionSecret string) {
	if repo == nil || generate == nil || sessionSecret == "" {
		return
	}
	r.dkim = &adminDkimAPI{repo: repo, generate: generate, sessions: NewWebSessionVerifier(sessionSecret)}
}

func (r *Router) handleAdminDkimKeys(w http.ResponseWriter, req *http.Request) {
	if r.dkim == nil {
		writeError(w, http.StatusServiceUnavailable, "DKIM_API_DISABLED",
			"DKIM management needs JWT_SECRET and a master key")
		return
	}
	r.dkim.handleKeys(w, req)
}

func (r *Router) handleAdminDkimRotate(w http.ResponseWriter, req *http.Request) {
	if r.dkim == nil {
		writeError(w, http.StatusServiceUnavailable, "DKIM_API_DISABLED",
			"DKIM management needs JWT_SECRET and a master key")
		return
	}
	r.dkim.handleRotate(w, req)
}
