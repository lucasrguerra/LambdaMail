package httppresentation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/emersion/go-message"

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

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/mail/message/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "INVALID_UID", "Message UID is required")
		return
	}

	uid, err := strconv.ParseUint(parts[0], 10, 32)
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

	// Handle attachment subpath: /api/v1/mail/message/{uid}/attachment/{filename}
	if len(parts) >= 3 && parts[1] == "attachment" {
		m.handleAttachmentDownload(w, r, raw, parts[2])
		return
	}

	// Parsed here rather than in the browser: this service already depends on
	// a MIME library, and a second parser in TypeScript would be a second set
	// of edge cases to get wrong.
	writeJSON(w, http.StatusOK, usecase.RenderMessage(raw, uint32(uid)))
}

func (m *mailAPI) handleAttachmentDownload(w http.ResponseWriter, _ *http.Request, raw []byte, filename string) {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Attachment not found")
		return
	}

	data, contentType := usecase.ExtractAttachment(entity, filename)
	if data == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Attachment not found")
		return
	}

	// The sender chose this filename and this content type, so neither is
	// trusted. Serving an attachment's own text/html back on the webmail's
	// origin is stored XSS, and a quote in the name breaks out of the
	// Content-Disposition parameter - both reachable by anyone who can send
	// this mailbox a message.
	w.Header().Set("Content-Type", safeAttachmentContentType(contentType))
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": sanitiseFilename(filename)}))
	// Without nosniff a browser may still sniff the bytes and render them as
	// HTML regardless of the declared type.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// renderableTypes are the content types a browser will execute or render in a
// document context. They are downgraded so an attachment can never become a
// page on this origin.
var renderableTypes = map[string]bool{
	"text/html":                     true,
	"application/xhtml+xml":         true,
	"image/svg+xml":                 true,
	"application/xml":               true,
	"text/xml":                      true,
	"application/javascript":        true,
	"text/javascript":               true,
	"application/x-javascript":      true,
	"application/xhtml":             true,
	"application/vnd.wap.xhtml+xml": true,
}

func safeAttachmentContentType(declared string) string {
	declared = strings.ToLower(strings.TrimSpace(declared))
	if declared == "" || renderableTypes[declared] {
		return "application/octet-stream"
	}
	return declared
}

// sanitiseFilename keeps a name usable in a header and on a filesystem: no
// path separators, no control characters, no quotes.
func sanitiseFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return -1
		case r == '"' || r == '\\' || r == '/' || r == ':':
			return '_'
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
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

	input, ok := m.decodeCompose(w, r, session.Email)
	if !ok {
		return
	}

	if err := m.useCase.Send(r.Context(), input); err != nil {
		m.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

// handleDelete removes one message: to Trash from anywhere else, and for good
// from Trash itself.
func (m *mailAPI) handleDelete(w http.ResponseWriter, r *http.Request) {
	session, ok := m.authenticate(w, r)
	if !ok {
		return
	}

	var body struct {
		Folder string `json:"folder"`
		UID    uint32 `json:"uid"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return
	}
	if body.Folder == "" {
		body.Folder = "inbox"
	}
	if body.UID == 0 {
		writeError(w, http.StatusBadRequest, "INVALID_UID", "Message UID is required")
		return
	}

	if err := m.useCase.Delete(r.Context(), session.Email, body.Folder, body.UID); err != nil {
		m.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// handleSaveDraft stores the message being composed in the Drafts folder.
func (m *mailAPI) handleSaveDraft(w http.ResponseWriter, r *http.Request) {
	session, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	// The body is read once, so replace_uid has to come out of the same decode.
	input, replaceUID, ok := m.decodeDraft(w, r, session.Email)
	if !ok {
		return
	}

	uid, err := m.useCase.SaveDraft(r.Context(), input, replaceUID)
	if err != nil {
		m.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "uid": uid})
}

// decodeCompose reads a composed message off the wire.
//
// "html" and "text" are both accepted, and both are new: the field was called
// "body" while the composer had always sent "html", so every message sent from
// the webmail was delivered with an empty body. "bcc" was likewise never read,
// so blind recipients were silently dropped.
func (m *mailAPI) decodeCompose(
	w http.ResponseWriter, r *http.Request, sender string,
) (usecase.ComposeInput, bool) {
	var body struct {
		To      []string `json:"to"`
		Cc      []string `json:"cc"`
		Bcc     []string `json:"bcc"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    string   `json:"html"`
		// Accepted so an older client - or a script written against the
		// previous shape - still sends a readable message rather than a blank
		// one. Only used when neither text nor html is present.
		LegacyBody string `json:"body"`
		// DraftUID is the autosaved draft this message came from, so sending
		// can clear it. Without it the draft outlived the message it became.
		DraftUID uint32 `json:"draft_uid"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxComposeBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
		return usecase.ComposeInput{}, false
	}

	text := body.Text
	if text == "" && body.HTML == "" {
		text = body.LegacyBody
	}

	// The sender is the session's address, never a field in the request:
	// letting the body choose From is how a webmail becomes an open relay for
	// spoofed mail.
	return usecase.ComposeInput{
		From:     sender,
		To:       body.To,
		Cc:       body.Cc,
		Bcc:      body.Bcc,
		Subject:  body.Subject,
		Text:     text,
		HTML:     body.HTML,
		DraftUID: body.DraftUID,
	}, true
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
	case errors.Is(err, usecase.ErrDraftsUnavailable):
		writeError(w, http.StatusServiceUnavailable, "DRAFTS_UNAVAILABLE", "Draft storage is not configured")
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Request could not be processed")
	}
}

// decodeDraft reads a composed message plus the draft it supersedes.
//
// replace_uid carries the UID of the previous autosave so the new one takes
// its place. Without it each keystroke pause would leave another copy behind
// and the Drafts folder would fill with the same unfinished message.
func (m *mailAPI) decodeDraft(
	w http.ResponseWriter, r *http.Request, sender string,
) (usecase.ComposeInput, uint32, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxComposeBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Could not read the request")
		return usecase.ComposeInput{}, 0, false
	}

	var meta struct {
		ReplaceUID uint32 `json:"replace_uid"`
	}
	// A missing or malformed replace_uid is not fatal: the draft is still
	// worth saving, it just will not supersede anything.
	_ = json.Unmarshal(raw, &meta)

	r.Body = io.NopCloser(bytes.NewReader(raw))
	input, ok := m.decodeCompose(w, r, sender)
	return input, meta.ReplaceUID, ok
}
