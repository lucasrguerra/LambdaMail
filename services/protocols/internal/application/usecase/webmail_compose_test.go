package usecase

import (
	"context"
	"errors"
	"strconv"
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
	mailboxID    string
	expunged     []uint32
	expungeErr   error
	movedToTrash []uint32
	moveErr      error
	moved        []string
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
func (s *stubWebmailRepo) MoveToFolder(
	_ context.Context, _, from string, uid uint32, to string,
) (uint32, error) {
	if s.moveErr != nil {
		return 0, s.moveErr
	}
	s.moved = append(s.moved, from+"->"+to+"/"+strconv.FormatUint(uint64(uid), 10))
	return uid, nil
}

func (s *stubWebmailRepo) MoveToTrash(_ context.Context, _, _ string, uid uint32) (uint32, error) {
	if s.moveErr != nil {
		return 0, s.moveErr
	}
	s.movedToTrash = append(s.movedToTrash, uid)
	return uid, nil
}

// newSendableUseCase wires the smallest use case that can actually complete a
// Send: a real submission path over recording stubs.
func newSendableUseCase(t *testing.T, mailboxID uuid.UUID, repo *stubWebmailRepo) *WebmailUseCase {
	t.Helper()
	return newSendableUseCaseWith(t, mailboxID, repo, &recordingMessageRepository{})
}

func newSendableUseCaseWith(
	t *testing.T, mailboxID uuid.UUID, repo *stubWebmailRepo, messages *recordingMessageRepository,
) *WebmailUseCase {
	t.Helper()
	account := &port.MailboxAuth{
		ID: mailboxID, EmailAddress: "me@example.test",
		DomainName: "example.test", MaxRecipientsPerHour: 100,
	}
	submission := NewProcessOutboundEmailUseCase(
		nil, &countingOutboundRepo{}, &capturingBlobStorage{}, &recordingSigner{})
	return (&WebmailUseCase{
		repo:       repo,
		auth:       &stubAuthRepo{account: account},
		submission: submission,
		localHost:  "mail.example.test",
	}).WithLocalFiling(
		&stubBlobStorage{ref: port.BlobRef{ID: uuid.New(), SHA256: "d", SizeBytes: 5}}, messages)
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

// --- sending, and what it leaves behind ----------------------------------

// The bug as the user hit it: write a message, send it, and the draft the
// autosave had stored was still sitting in Drafts afterwards - a duplicate of
// a message already on its way, that nothing on any screen could remove.
func TestSendingDiscardsTheDraftItCameFrom(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String()}
	uc := newSendableUseCase(t, mailboxID, repo)

	if err := uc.Send(context.Background(), ComposeInput{
		From:     "me@example.test",
		To:       []string{"you@example.test"},
		Subject:  "Finished",
		HTML:     "<p>done</p>",
		DraftUID: 42,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(repo.expunged) != 1 || repo.expunged[0] != 42 {
		t.Errorf("the draft was left in Drafts after sending: expunged %v, want [42]", repo.expunged)
	}
}

// A message composed from scratch has no draft behind it, and must not try to
// remove UID 0 - which is not a UID at all.
func TestSendingWithoutADraftExpungesNothing(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String()}
	uc := newSendableUseCase(t, mailboxID, repo)

	if err := uc.Send(context.Background(), ComposeInput{
		From: "me@example.test", To: []string{"you@example.test"}, Text: "hi",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(repo.expunged) != 0 {
		t.Errorf("nothing should have been expunged, got %v", repo.expunged)
	}
}

// The Sent copy is a message the user wrote; it is not unread mail, and filing
// it as unread is what left the Sent folder with a badge nothing could clear.
func TestSentCopyIsFiledAsAlreadyRead(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String()}
	messages := &recordingMessageRepository{}
	uc := newSendableUseCaseWith(t, mailboxID, repo, messages)

	if err := uc.Send(context.Background(), ComposeInput{
		From: "me@example.test", To: []string{"you@example.test"}, Text: "hi",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(messages.persisted) != 1 {
		t.Fatalf("want one filed copy, got %d", len(messages.persisted))
	}
	if !messages.persisted[0].AlreadySeen {
		t.Error("the Sent copy was filed as unread mail")
	}
}

// Likewise a draft: nothing ever opens one, so if it is filed unread its
// folder carries a badge forever.
func TestDraftIsFiledAsAlreadyRead(t *testing.T) {
	mailboxID := uuid.New()
	messages := &recordingMessageRepository{}
	uc := (&WebmailUseCase{
		repo:      &stubWebmailRepo{mailboxID: mailboxID.String()},
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}).WithLocalFiling(
		&stubBlobStorage{ref: port.BlobRef{ID: uuid.New(), SHA256: "d", SizeBytes: 5}}, messages)

	if _, err := uc.SaveDraft(context.Background(), ComposeInput{
		From: "me@example.test", Subject: "half",
	}, 0); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if len(messages.persisted) != 1 || !messages.persisted[0].AlreadySeen {
		t.Error("the draft was filed as unread mail")
	}
}

// --- deleting ------------------------------------------------------------

// Deleting from an ordinary folder moves the message to Trash.
func TestDeleteMovesToTrash(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String()}
	uc := &WebmailUseCase{
		repo:      repo,
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}

	if err := uc.Delete(context.Background(), "me@example.test", "inbox", 7); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(repo.movedToTrash) != 1 || repo.movedToTrash[0] != 7 {
		t.Errorf("the message was not moved to Trash: %v", repo.movedToTrash)
	}
	if len(repo.expunged) != 0 {
		t.Errorf("deleting from the inbox must not destroy the message: expunged %v", repo.expunged)
	}
}

// Deleting from Trash is the second press, and that one really removes it.
func TestDeleteFromTrashExpunges(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String(), moveErr: port.ErrAlreadyInTrash}
	uc := &WebmailUseCase{
		repo:      repo,
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}

	if err := uc.Delete(context.Background(), "me@example.test", "trash", 3); err != nil {
		t.Fatalf("delete from trash: %v", err)
	}
	if len(repo.expunged) != 1 || repo.expunged[0] != 3 {
		t.Errorf("emptying from Trash did not remove the message: %v", repo.expunged)
	}
}

// --- moving between folders ---------------------------------------------

// Filing a message somewhere else is the one ordinary mail action the webmail
// still had no way to perform.
func TestMoveFilesTheMessageInTheTargetFolder(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String()}
	uc := &WebmailUseCase{
		repo:      repo,
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}

	if err := uc.Move(context.Background(), "me@example.test", "inbox", 5, "Archive"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if len(repo.moved) != 1 || repo.moved[0] != "inbox->Archive/5" {
		t.Errorf("moved %v", repo.moved)
	}
}

// Sent and Drafts describe how a message came to exist, not where the reader
// filed it. Moving arbitrary mail into them would make the folder lie: Sent
// would hold messages that were never sent, and a draft that cannot be edited
// would sit among the unfinished ones.
func TestMoveRefusesSentAndDrafts(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String()}
	uc := &WebmailUseCase{
		repo:      repo,
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}

	for _, target := range []string{"Sent", "sent", "Drafts", "DRAFTS"} {
		err := uc.Move(context.Background(), "me@example.test", "inbox", 5, target)
		if !errors.Is(err, ErrFolderNotATarget) {
			t.Errorf("moving into %q returned %v, want ErrFolderNotATarget", target, err)
		}
	}
	if len(repo.moved) != 0 {
		t.Errorf("a refused move still touched the repository: %v", repo.moved)
	}
}

// Moving a message into the folder it is already in is a no-op, not an error:
// it is what a double click on the same target produces.
func TestMoveIntoTheSameFolderDoesNothing(t *testing.T) {
	mailboxID := uuid.New()
	repo := &stubWebmailRepo{mailboxID: mailboxID.String()}
	uc := &WebmailUseCase{
		repo:      repo,
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}

	if err := uc.Move(context.Background(), "me@example.test", "inbox", 5, "inbox"); err != nil {
		t.Fatalf("move into the same folder: %v", err)
	}
	if len(repo.moved) != 0 {
		t.Errorf("a no-op move still touched the repository: %v", repo.moved)
	}
}

func TestMoveRejectsAnEmptyTarget(t *testing.T) {
	mailboxID := uuid.New()
	uc := &WebmailUseCase{
		repo:      &stubWebmailRepo{mailboxID: mailboxID.String()},
		auth:      &stubAuthRepo{account: &port.MailboxAuth{ID: mailboxID, EmailAddress: "me@example.test"}},
		localHost: "mail.example.test",
	}
	if err := uc.Move(context.Background(), "me@example.test", "inbox", 5, "  "); err == nil {
		t.Error("an empty target should be refused")
	}
}
