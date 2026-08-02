package usecase

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// Every one of these reproduces a way the composer's mail was being mangled or
// lost between the browser and the queue.

func TestComposedMessageCarriesTheBody(t *testing.T) {
	// The HTTP layer read a field called "body" while the composer had always
	// sent "html", so the message that reached this function had an empty body
	// and every mail sent from the webmail arrived blank.
	payload := string(buildMimeMessage(ComposeInput{
		From:    "me@example.test",
		To:      []string{"you@example.test"},
		Subject: "Hello",
		HTML:    "<p>Real content</p>",
	}, []string{"you@example.test"}, "mail.example.test"))

	if !strings.Contains(payload, "Real content") {
		t.Fatalf("the body is missing from the message:\n%s", payload)
	}
}

func TestHtmlIsSentAsHtml(t *testing.T) {
	payload := string(buildMimeMessage(ComposeInput{
		From:    "me@example.test",
		To:      []string{"you@example.test"},
		Subject: "Formatted",
		HTML:    "<p>Hello <b>there</b></p>",
	}, []string{"you@example.test"}, "mail.example.test"))

	if !strings.Contains(payload, "multipart/alternative") {
		t.Errorf("want multipart/alternative for an HTML message, got:\n%s", payload)
	}
	if !strings.Contains(payload, "Content-Type: text/html; charset=utf-8") {
		t.Errorf("the HTML part is not declared as text/html:\n%s", payload)
	}
	// A plain-text alternative must exist and must not be empty: an
	// HTML-only multipart message scores badly with every spam filter.
	if !strings.Contains(payload, "Content-Type: text/plain; charset=utf-8") {
		t.Errorf("no plain-text alternative:\n%s", payload)
	}
	if !strings.Contains(payload, "Hello there") {
		t.Errorf("the plain-text alternative did not get a readable fallback:\n%s", payload)
	}
	// text/plain must come first - least preferred first, RFC 2046 5.1.4.
	if strings.Index(payload, "text/plain") > strings.Index(payload, "text/html") {
		t.Error("text/plain must precede text/html in multipart/alternative")
	}
}

func TestPlainTextOnlyMessageStaysSimple(t *testing.T) {
	payload := string(buildMimeMessage(ComposeInput{
		From:    "me@example.test",
		To:      []string{"you@example.test"},
		Subject: "Plain",
		Text:    "just words",
	}, []string{"you@example.test"}, "mail.example.test"))

	if strings.Contains(payload, "multipart") {
		t.Errorf("a text-only message should not be multipart:\n%s", payload)
	}
	if !strings.Contains(payload, "just words") {
		t.Errorf("body missing:\n%s", payload)
	}
}

func TestBccNeverAppearsInTheHeaders(t *testing.T) {
	// Bcc belongs to the envelope alone. Leaking it into the headers would
	// show every recipient who else was blind-copied.
	input := ComposeInput{
		From:    "me@example.test",
		To:      []string{"you@example.test"},
		Bcc:     []string{"secret@example.test"},
		Subject: "Quiet",
		Text:    "hi",
	}
	recipients := normaliseRecipients(
		append(append(append([]string{}, input.To...), input.Cc...), input.Bcc...))

	// It does reach the envelope, or the blind copy is simply not delivered -
	// which is what happened while Bcc was not read from the request at all.
	found := false
	for _, r := range recipients {
		if r == "secret@example.test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("bcc recipient missing from the envelope: %v", recipients)
	}

	payload := string(buildMimeMessage(input, recipients, "mail.example.test"))
	headers, _, _ := strings.Cut(payload, "\r\n\r\n")
	if strings.Contains(strings.ToLower(headers), "bcc") ||
		strings.Contains(headers, "secret@example.test") {
		t.Errorf("bcc leaked into the headers:\n%s", headers)
	}
}

func TestHtmlToPlainTextIsReadable(t *testing.T) {
	got := htmlToPlainText("<p>First line</p><p>Second &amp; last</p><br><ul><li>one</li><li>two</li></ul>")
	for _, want := range []string{"First line", "Second & last", "- one", "- two"} {
		if !strings.Contains(got, want) {
			t.Errorf("plain-text fallback missing %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<") || strings.Contains(got, "&amp;") {
		t.Errorf("markup survived into the plain-text part:\n%s", got)
	}
}

// --- draft storage -------------------------------------------------------

type stubWebmailRepo struct {
	mailboxID  string
	expunged   []uint32
	expungeErr error
}

func (s *stubWebmailRepo) FindMailboxIDByAddress(context.Context, string) (string, error) {
	return s.mailboxID, nil
}
func (s *stubWebmailRepo) ListFolders(context.Context, string) ([]port.WebmailFolder, error) {
	return nil, nil
}
func (s *stubWebmailRepo) ListMessages(context.Context, string, string, string, int, int) ([]port.WebmailMessage, error) {
	return nil, nil
}
func (s *stubWebmailRepo) GetMessageBlob(context.Context, string, string, uint32) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (s *stubWebmailRepo) MarkSeen(context.Context, string, string, uint32, bool) error { return nil }
func (s *stubWebmailRepo) Expunge(_ context.Context, _, _ string, uid uint32) error {
	s.expunged = append(s.expunged, uid)
	return s.expungeErr
}

type stubAuthRepo struct{ account *port.MailboxAuth }

func (s *stubAuthRepo) FindByAddress(context.Context, string) (*port.MailboxAuth, error) {
	return s.account, nil
}

// Autosaving must replace the previous draft, not pile up another copy of the
// same half-written message every time the typing pauses.
func TestDraftAutosaveReplacesThePreviousOne(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String()}
	messages := &recordingMessageRepository{}
	blobs := &stubBlobStorage{ref: port.BlobRef{ID: uuid.New(), SHA256: "d", SizeBytes: 5}}

	uc := (&WebmailUseCase{
		repo:      repo,
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}).WithLocalFiling(blobs, messages)

	first, err := uc.SaveDraft(context.Background(), ComposeInput{
		From: "me@example.test", Subject: "Half", HTML: "<p>writ</p>",
	}, 0)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	if len(repo.expunged) != 0 {
		t.Errorf("the first save has nothing to replace, expunged %v", repo.expunged)
	}

	if _, err := uc.SaveDraft(context.Background(), ComposeInput{
		From: "me@example.test", Subject: "Half", HTML: "<p>written</p>",
	}, first); err != nil {
		t.Fatalf("second save: %v", err)
	}

	if len(repo.expunged) != 1 || repo.expunged[0] != first {
		t.Errorf("the superseded draft was not removed: expunged %v, want [%d]", repo.expunged, first)
	}
	if len(messages.persisted) != 2 {
		t.Fatalf("want 2 writes, got %d", len(messages.persisted))
	}
	if messages.persisted[0].TargetFolderName != "Drafts" {
		t.Errorf("draft filed in %q, want Drafts", messages.persisted[0].TargetFolderName)
	}
}

// A draft is a half-written message by definition; it must save with no
// recipients, where Send would rightly refuse.
func TestDraftSavesWithoutRecipients(t *testing.T) {
	mailboxID := uuid.New()
	uc := (&WebmailUseCase{
		repo:      &stubWebmailRepo{mailboxID: mailboxID.String()},
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}).WithLocalFiling(
		&stubBlobStorage{ref: port.BlobRef{ID: uuid.New(), SHA256: "d", SizeBytes: 5}},
		&recordingMessageRepository{},
	)

	if _, err := uc.SaveDraft(context.Background(), ComposeInput{
		From: "me@example.test", Subject: "no recipients yet",
	}, 0); err != nil {
		t.Fatalf("a draft with no recipients must still save: %v", err)
	}
}
