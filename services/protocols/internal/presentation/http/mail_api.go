package httppresentation

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/postgres"
)

// maxComposeBytes caps a single submission from the webmail. The SMTP ports
// have their own limit; this one stops an authenticated browser session from
// buffering an unbounded body in this process.
const maxComposeBytes = 32 << 20 // 32 MiB

// mailAPI serves /api/v1/mail/* for the webmail's message screens.
type mailAPI struct {
	useCase  *usecase.WebmailUseCase
	sessions *WebSessionVerifier
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

// authenticate resolves the caller's session, refusing anything that is not a
// /user session token signed by the auth service.
func (m *mailAPI) authenticate(w http.ResponseWriter, r *http.Request) (*WebSession, bool) {
	token := ""
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		token = strings.TrimPrefix(header, "Bearer ")
	} else if cookie, err := r.Cookie("lm_user_session"); err == nil {
		token = cookie.Value
	}

	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "User session required")
		return nil, false
	}

	session, err := m.sessions.RequireSurface(token, "user")
	if err != nil {
		// The reason is deliberately not echoed back: whether a token expired,
		// was forged or belongs to the other surface is not the caller's
		// business.
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "User session required")
		return nil, false
	}
	return session, true
}

func (m *mailAPI) handleFolders(w http.ResponseWriter, r *http.Request) {
	session, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	folders, err := m.useCase.ListFolders(r.Context(), session.Email)
	if err != nil {
		m.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, folders)
}

func (m *mailAPI) handleMessages(w http.ResponseWriter, r *http.Request) {
	session, ok := m.authenticate(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	folder := query.Get("folder")
	if folder == "" {
		folder = "inbox"
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))

	messages, err := m.useCase.ListMessages(r.Context(), session.Email, folder, query.Get("q"), limit, offset)
	if err != nil {
		m.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, messages)
}

func (m *mailAPI) handleMessage(w http.ResponseWriter, r *http.Request) {
	session, ok := m.authenticate(w, r)
	if !ok {
		return
	}

	uid, err := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/api/v1/mail/message/"), 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_UID", "Message UID must be a number")
		return
	}

	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "inbox"
	}

	raw, err := m.useCase.GetMessage(r.Context(), session.Email, folder, uint32(uid))
	if err != nil {
		m.fail(w, err)
		return
	}

	// Parsed here rather than in the browser: this service already depends on
	// a MIME library, and a second parser in TypeScript would be a second set
	// of edge cases to get wrong.
	writeJSON(w, http.StatusOK, usecase.RenderMessage(raw, uint32(uid)))
}

func (m *mailAPI) handleSeen(w http.ResponseWriter, r *http.Request) {
	session, ok := m.authenticate(w, r)
	if !ok {
		return
	}

	var body struct {
		Folder string `json:"folder"`
		UID    uint32 `json:"uid"`
		Seen   bool   `json:"seen"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}
	if body.Folder == "" {
		body.Folder = "inbox"
	}

	if err := m.useCase.SetSeen(r.Context(), session.Email, body.Folder, body.UID, body.Seen); err != nil {
		m.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (m *mailAPI) handleSend(w http.ResponseWriter, r *http.Request) {
	session, ok := m.authenticate(w, r)
	if !ok {
		return
	}

	var body struct {
		To      []string `json:"to"`
		Cc      []string `json:"cc"`
		Subject string   `json:"subject"`
		Body    string   `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxComposeBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}

	// The sender is the session's address, never a field in the request:
	// letting the body choose From is how a webmail becomes an open relay for
	// spoofed mail.
	err := m.useCase.Send(r.Context(), usecase.ComposeInput{
		From:    session.Email,
		To:      body.To,
		Cc:      body.Cc,
		Subject: body.Subject,
		Body:    body.Body,
	})
	if err != nil {
		m.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

// fail maps domain errors onto status codes without leaking internals.
func (m *mailAPI) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrMessageNotFound), errors.Is(err, usecase.ErrNoSuchMailbox):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Message not found")
	case errors.Is(err, usecase.ErrNoRecipients):
		writeError(w, http.StatusBadRequest, "NO_RECIPIENTS", "At least one valid recipient is required")
	case errors.Is(err, usecase.ErrSendLimitExceeded):
		writeError(w, http.StatusTooManyRequests, "SEND_LIMIT_EXCEEDED", "Hourly recipient limit reached")
	case errors.Is(err, usecase.ErrSenderNotOwned):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "Sender address does not belong to this account")
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Request could not be processed")
	}
}
