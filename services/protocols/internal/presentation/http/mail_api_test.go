package httppresentation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/postgres"
)

// The webmail's HTTP surface: the only part of this server a browser talks to
// directly, and the one that decides whose mail a session may touch.
//
// None of it was covered. Every one of these asserts something a mistake here
// would cost: another account's mail, an attachment rendered as a page on this
// origin, or a sender address chosen by the request rather than the session.

// --- test doubles --------------------------------------------------------

// fakeMailStore records what the API asked for, so a handler can be checked
// on the arguments it passed rather than only on its status code.
type fakeMailStore struct {
	// byMailbox is the mail each mailbox owns, keyed by mailbox ID.
	byMailbox map[string][]port.WebmailMessage
	addresses map[string]string // address -> mailbox ID

	seenCalls    []string
	deletedCalls []string
	movedCalls   []string
	expungeErr   error
	moveErr      error
	blob         uuid.UUID
}

func (f *fakeMailStore) FindMailboxIDByAddress(_ context.Context, address string) (string, error) {
	id, ok := f.addresses[address]
	if !ok {
		return "", postgres.ErrMessageNotFound
	}
	return id, nil
}

func (f *fakeMailStore) ListFolders(_ context.Context, mailboxID string) ([]port.WebmailFolder, error) {
	return []port.WebmailFolder{{
		ID: "f1", Name: "INBOX", SpecialUse: "inbox",
		TotalCount: len(f.byMailbox[mailboxID]),
	}}, nil
}

func (f *fakeMailStore) ListMessages(
	_ context.Context, mailboxID, _, _ string, _, _ int,
) ([]port.WebmailMessage, error) {
	return f.byMailbox[mailboxID], nil
}

func (f *fakeMailStore) GetMessageBlob(
	_ context.Context, mailboxID, _ string, uid uint32,
) (uuid.UUID, error) {
	for _, m := range f.byMailbox[mailboxID] {
		if m.UID == uid {
			return f.blob, nil
		}
	}
	// Exactly what the real repository does for a UID this mailbox does not
	// own: not found, indistinguishable from one that does not exist.
	return uuid.Nil, postgres.ErrMessageNotFound
}

func (f *fakeMailStore) MarkSeen(_ context.Context, mailboxID, folder string, uid uint32, seen bool) error {
	f.seenCalls = append(f.seenCalls, mailboxID+"/"+folder)
	return nil
}

func (f *fakeMailStore) Expunge(_ context.Context, mailboxID, folder string, uid uint32) error {
	if f.expungeErr != nil {
		return f.expungeErr
	}
	f.expungeErr = nil
	f.deletedCalls = append(f.deletedCalls, mailboxID+"/"+folder)
	return nil
}

func (f *fakeMailStore) MoveToTrash(
	_ context.Context, mailboxID, folder string, uid uint32,
) (uint32, error) {
	if f.moveErr != nil {
		return 0, f.moveErr
	}
	f.movedCalls = append(f.movedCalls, mailboxID+"/"+folder)
	return uid, nil
}

type fakeBlobReader struct{ raw []byte }

func (f *fakeBlobReader) ReadByID(context.Context, uuid.UUID) ([]byte, error) {
	return f.raw, nil
}

// --- harness -------------------------------------------------------------

// newMailAPI builds the API over fakes, pinned to an instant inside the
// captured tokens' validity window so these tests never age out.
func newMailAPI(t *testing.T, store *fakeMailStore, raw []byte) *mailAPI {
	t.Helper()
	uc := usecase.NewWebmailUseCase(store, &fakeBlobReader{raw: raw}, nil, nil, "mail.example.test")
	return &mailAPI{useCase: uc, sessions: testVerifier(interopSecret)}
}

// The session in interopSession belongs to this address.
const sessionOwner = "user@example.test"

func request(method, target string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+interopSession)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func storeWithMail() *fakeMailStore {
	return &fakeMailStore{
		addresses: map[string]string{sessionOwner: "mailbox-1", "other@example.test": "mailbox-2"},
		byMailbox: map[string][]port.WebmailMessage{
			"mailbox-1": {{UID: 1, Subject: "mine"}},
			"mailbox-2": {{UID: 99, Subject: "somebody else's"}},
		},
		blob: uuid.New(),
	}
}

// --- authentication ------------------------------------------------------

func TestMailApiRefusesARequestWithNoSession(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	for name, req := range map[string]*http.Request{
		"folders":  httptest.NewRequest(http.MethodGet, "/api/v1/mail/folders", nil),
		"messages": httptest.NewRequest(http.MethodGet, "/api/v1/mail/messages", nil),
		"seen":     httptest.NewRequest(http.MethodPost, "/api/v1/mail/seen", strings.NewReader(`{"uid":1}`)),
		"delete":   httptest.NewRequest(http.MethodPost, "/api/v1/mail/delete", strings.NewReader(`{"uid":1}`)),
	} {
		rec := httptest.NewRecorder()
		switch name {
		case "folders":
			api.handleFolders(rec, req)
		case "messages":
			api.handleMessages(rec, req)
		case "seen":
			api.handleSeen(rec, req)
		case "delete":
			api.handleDelete(rec, req)
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d without a session, want 401", name, rec.Code)
		}
	}
}

// An admin session must not reach the mail API. The two surfaces are separated
// by audience precisely so a console token cannot read a mailbox.
func TestMailApiRefusesAnAdminSession(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mail/folders", nil)
	req.Header.Set("Authorization", "Bearer "+adminSurfaceToken(t))
	rec := httptest.NewRecorder()
	api.handleFolders(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an admin token reached the mail API: %d", rec.Code)
	}
}

// The half-issued token from between the password and the second factor is not
// a session and must not be accepted as one.
func TestMailApiRefusesAnMfaChallengeToken(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mail/folders", nil)
	req.Header.Set("Authorization", "Bearer "+interopChallenge)
	rec := httptest.NewRecorder()
	api.handleFolders(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an MFA challenge token was accepted as a session: %d", rec.Code)
	}
}

// A tampered signature must fail, or the secret is doing nothing.
func TestMailApiRefusesATamperedToken(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mail/folders", nil)
	req.Header.Set("Authorization", "Bearer "+interopSession[:len(interopSession)-4]+"AAAA")
	rec := httptest.NewRecorder()
	api.handleFolders(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a token with a broken signature was accepted: %d", rec.Code)
	}
}

// The cookie is the browser's normal path and must work like the header.
func TestMailApiAcceptsTheSessionCookie(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/mail/folders", nil)
	req.AddCookie(&http.Cookie{Name: "lm_user_session", Value: interopSession})
	rec := httptest.NewRecorder()
	api.handleFolders(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("the session cookie was not accepted: %d", rec.Code)
	}
}

// --- isolation between accounts -----------------------------------------

// The mailbox is resolved from the session's address, never from the request.
// This is the check that keeps one account out of another's mail.
func TestMailApiServesOnlyTheSessionsOwnMailbox(t *testing.T) {
	store := storeWithMail()
	api := newMailAPI(t, store, nil)

	rec := httptest.NewRecorder()
	api.handleMessages(rec, request(http.MethodGet, "/api/v1/mail/messages", ""))

	var got []port.WebmailMessage
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range got {
		if m.Subject == "somebody else's" {
			t.Fatal("the API served another mailbox's mail")
		}
	}
	if len(got) != 1 || got[0].Subject != "mine" {
		t.Errorf("got %+v, want only this session's own message", got)
	}
}

// Naming another mailbox's UID must read as not-found, not as that message.
func TestMailApiCannotReadAnotherMailboxesMessageByUid(t *testing.T) {
	store := storeWithMail()
	api := newMailAPI(t, store, []byte("Subject: secret\r\n\r\nbody"))

	rec := httptest.NewRecorder()
	// UID 99 exists, but in mailbox-2.
	api.handleMessage(rec, request(http.MethodGet, "/api/v1/mail/message/99", ""))

	if rec.Code != http.StatusNotFound {
		t.Errorf("reading another mailbox's UID answered %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Error("another mailbox's message body was served")
	}
}

// --- reading -------------------------------------------------------------

// Opening a message marks it read; that is what moves the unread counter.
func TestOpeningAMessageMarksItRead(t *testing.T) {
	store := storeWithMail()
	api := newMailAPI(t, store, []byte("Subject: hi\r\n\r\nbody"))

	rec := httptest.NewRecorder()
	api.handleMessage(rec, request(http.MethodGet, "/api/v1/mail/message/1?folder=inbox", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("reading own message answered %d", rec.Code)
	}
	if len(store.seenCalls) != 1 || store.seenCalls[0] != "mailbox-1/inbox" {
		t.Errorf("marked seen on %v, want exactly [mailbox-1/inbox]", store.seenCalls)
	}
}

// The folder defaults rather than 400-ing, because the list view omits it.
func TestMessageListDefaultsToTheInbox(t *testing.T) {
	store := storeWithMail()
	api := newMailAPI(t, store, nil)

	rec := httptest.NewRecorder()
	api.handleSeen(rec, request(http.MethodPost, "/api/v1/mail/seen", `{"uid":1,"seen":true}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d", rec.Code)
	}
	if len(store.seenCalls) != 1 || store.seenCalls[0] != "mailbox-1/inbox" {
		t.Errorf("the folder did not default to inbox: %v", store.seenCalls)
	}
}

// --- deleting ------------------------------------------------------------

func TestDeleteMovesToTrashFromAnOrdinaryFolder(t *testing.T) {
	store := storeWithMail()
	api := newMailAPI(t, store, nil)

	rec := httptest.NewRecorder()
	api.handleDelete(rec, request(http.MethodPost, "/api/v1/mail/delete", `{"folder":"inbox","uid":1}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("delete answered %d: %s", rec.Code, rec.Body)
	}
	// The mailbox is asserted, not just the call: counting calls alone would
	// pass just as happily if the delete acted on somebody else's mailbox.
	if len(store.movedCalls) != 1 || store.movedCalls[0] != "mailbox-1/inbox" {
		t.Errorf("moved %v, want exactly [mailbox-1/inbox]", store.movedCalls)
	}
	if len(store.deletedCalls) != 0 {
		t.Errorf("deleting from the inbox destroyed the message: %v", store.deletedCalls)
	}
}

func TestDeleteFromTrashExpungesForGood(t *testing.T) {
	store := storeWithMail()
	store.moveErr = port.ErrAlreadyInTrash
	api := newMailAPI(t, store, nil)

	rec := httptest.NewRecorder()
	api.handleDelete(rec, request(http.MethodPost, "/api/v1/mail/delete", `{"folder":"trash","uid":1}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("delete from trash answered %d: %s", rec.Code, rec.Body)
	}
	if len(store.deletedCalls) != 1 || store.deletedCalls[0] != "mailbox-1/trash" {
		t.Errorf("expunged %v, want exactly [mailbox-1/trash]", store.deletedCalls)
	}
}

// A delete with no UID is a bug in the caller, not a request to delete UID 0.
func TestDeleteRejectsAMissingUid(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	api.handleDelete(rec, request(http.MethodPost, "/api/v1/mail/delete", `{"folder":"inbox"}`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("a delete with no UID answered %d, want 400", rec.Code)
	}
}

func TestDeleteRejectsMalformedJson(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	api.handleDelete(rec, request(http.MethodPost, "/api/v1/mail/delete", `{not json`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON answered %d, want 400", rec.Code)
	}
}

// --- error mapping -------------------------------------------------------

// Each domain error has to reach the browser as something the composer can act
// on. A send limit that arrives as a 500 tells the user to try again, which is
// the one thing that will not work.
func TestDomainErrorsMapToUsefulStatusCodes(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{postgres.ErrMessageNotFound, http.StatusNotFound},
		{usecase.ErrNoSuchMailbox, http.StatusNotFound},
		{usecase.ErrNoRecipients, http.StatusBadRequest},
		{usecase.ErrSendLimitExceeded, http.StatusTooManyRequests},
		{usecase.ErrSenderNotOwned, http.StatusForbidden},
		{usecase.ErrDraftsUnavailable, http.StatusServiceUnavailable},
		{errors.New("something unexpected"), http.StatusInternalServerError},
	}
	api := newMailAPI(t, storeWithMail(), nil)

	for _, tc := range cases {
		rec := httptest.NewRecorder()
		api.fail(rec, tc.err)
		if rec.Code != tc.want {
			t.Errorf("%v mapped to %d, want %d", tc.err, rec.Code, tc.want)
		}
		// The internals must not travel to the browser with it.
		if strings.Contains(rec.Body.String(), "something unexpected") {
			t.Errorf("an internal error message leaked to the client: %s", rec.Body)
		}
	}
}

// adminSurfaceToken mints a token for the admin surface under the same secret,
// to prove the audience check is what refuses it rather than the signature.
func adminSurfaceToken(t *testing.T) string {
	t.Helper()
	return mintSession(t, WebSession{
		Subject: "mb-1", Email: sessionOwner, Role: "SUPER_ADMIN",
		Surface: "admin", Audience: "lambdamail:admin",
		Purpose: "session", MfaSatisfied: true,
		IssuedAt:  interopIssuedAt,
		ExpiresAt: interopIssuedAt + 3600,
	})
}

var _ = time.Now

// --- composing -----------------------------------------------------------

// The sender is taken from the session and never from the request body.
// Letting the body choose From is how a webmail becomes an open relay for
// spoofed mail, signed with this domain's own DKIM key.
func TestComposeAlwaysSendsAsTheSessionsOwnAddress(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	req := request(http.MethodPost, "/api/v1/mail/send", `{
		"from":"ceo@bank.example",
		"to":["victim@example.test"],
		"subject":"Wire transfer",
		"html":"<p>urgent</p>"
	}`)

	input, ok := api.decodeCompose(rec, req, sessionOwner)
	if !ok {
		t.Fatalf("decode refused a valid body: %s", rec.Body)
	}
	if input.From != sessionOwner {
		t.Errorf("From is %q; the request body chose the sender", input.From)
	}
}

// Bcc has to survive decoding, or a blind copy is silently dropped and nobody
// is told the message did not reach them.
func TestComposeKeepsBlindCopies(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	input, ok := api.decodeCompose(rec, request(http.MethodPost, "/api/v1/mail/send", `{
		"to":["a@example.test"],"cc":["b@example.test"],"bcc":["c@example.test"],"text":"hi"
	}`), sessionOwner)
	if !ok {
		t.Fatalf("decode refused: %s", rec.Body)
	}
	if len(input.Bcc) != 1 || input.Bcc[0] != "c@example.test" {
		t.Errorf("Bcc arrived as %v", input.Bcc)
	}
	if len(input.Cc) != 1 {
		t.Errorf("Cc arrived as %v", input.Cc)
	}
}

// The composer sends "html"; the field was once read as "body", and every
// message went out blank. The legacy name still has to work for anything
// written against the old shape.
func TestComposeAcceptsBothTheCurrentAndLegacyBodyFields(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	current, ok := api.decodeCompose(rec, request(http.MethodPost, "/x",
		`{"to":["a@example.test"],"html":"<p>real</p>"}`), sessionOwner)
	if !ok || current.HTML != "<p>real</p>" {
		t.Errorf("the html field did not survive: %+v", current)
	}

	rec = httptest.NewRecorder()
	legacy, ok := api.decodeCompose(rec, request(http.MethodPost, "/x",
		`{"to":["a@example.test"],"body":"plain words"}`), sessionOwner)
	if !ok || legacy.Text != "plain words" {
		t.Errorf("the legacy body field was dropped: %+v", legacy)
	}
}

// The draft a sent message came from has to reach the use case, or the draft
// outlives the message it became.
func TestComposeCarriesTheDraftItCameFrom(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	input, ok := api.decodeCompose(rec, request(http.MethodPost, "/api/v1/mail/send",
		`{"to":["a@example.test"],"text":"hi","draft_uid":77}`), sessionOwner)
	if !ok {
		t.Fatalf("decode refused: %s", rec.Body)
	}
	if input.DraftUID != 77 {
		t.Errorf("DraftUID is %d, want 77", input.DraftUID)
	}
}

// The draft decode reads the body once and has to get both halves out of it.
func TestDraftDecodeReadsBothTheMessageAndTheUidItReplaces(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	input, replaceUID, ok := api.decodeDraft(rec, request(http.MethodPost, "/api/v1/mail/draft",
		`{"to":["a@example.test"],"subject":"half","html":"<p>writ</p>","replace_uid":12}`), sessionOwner)
	if !ok {
		t.Fatalf("decode refused: %s", rec.Body)
	}
	if replaceUID != 12 {
		t.Errorf("replace_uid is %d, want 12", replaceUID)
	}
	if input.Subject != "half" || input.HTML != "<p>writ</p>" {
		t.Errorf("the draft body was lost: %+v", input)
	}
	if input.From != sessionOwner {
		t.Errorf("a draft's From is %q, want the session's address", input.From)
	}
}

// A draft with no replace_uid is the first save of a message and must still
// decode - it simply supersedes nothing.
func TestDraftDecodeToleratesAMissingReplaceUid(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	_, replaceUID, ok := api.decodeDraft(rec, request(http.MethodPost, "/api/v1/mail/draft",
		`{"subject":"first save"}`), sessionOwner)
	if !ok {
		t.Fatalf("the first save of a draft was refused: %s", rec.Body)
	}
	if replaceUID != 0 {
		t.Errorf("replace_uid is %d, want 0", replaceUID)
	}
}

func TestComposeRejectsMalformedJson(t *testing.T) {
	api := newMailAPI(t, storeWithMail(), nil)

	rec := httptest.NewRecorder()
	if _, ok := api.decodeCompose(rec, request(http.MethodPost, "/x", `{"to":`), sessionOwner); ok {
		t.Error("malformed JSON was accepted")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("answered %d, want 400", rec.Code)
	}
}
