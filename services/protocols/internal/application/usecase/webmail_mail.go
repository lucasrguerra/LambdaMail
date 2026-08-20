package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// ErrNoSuchMailbox is returned when the session names an address this server
// does not serve, which happens after an account is deleted while a token is
// still valid.
var ErrNoSuchMailbox = errors.New("webmail: no active mailbox for this session")

// WebmailUseCase serves the webmail's mail screens.
//
// Every operation starts from the address in the verified session and resolves
// it to a mailbox ID here, so no handler can be tricked into acting on an
// identifier that came from the request.
type WebmailUseCase struct {
	repo       port.WebmailRepository
	blobs      port.BlobReader
	submission *ProcessOutboundEmailUseCase
	auth       port.AuthRepository
	localHost  string
	// blobStore and messages file the sender's own copy into Sent, and hold
	// drafts. Both are optional: when either is nil the copy is skipped rather
	// than the send failing, because a message that has already been queued
	// for delivery must not be reported as an error.
	blobStore port.BlobStorage
	messages  port.InboundMessageRepository
}

func NewWebmailUseCase(
	repo port.WebmailRepository,
	blobs port.BlobReader,
	submission *ProcessOutboundEmailUseCase,
	auth port.AuthRepository,
	localHost string,
) *WebmailUseCase {
	if localHost == "" {
		localHost = "localhost"
	}
	return &WebmailUseCase{repo: repo, blobs: blobs, submission: submission, auth: auth, localHost: localHost}
}

// WithLocalFiling enables the Sent copy and draft storage.
func (uc *WebmailUseCase) WithLocalFiling(store port.BlobStorage, messages port.InboundMessageRepository) *WebmailUseCase {
	uc.blobStore = store
	uc.messages = messages
	return uc
}

// MailboxIDFor resolves a session's address to its mailbox, which the event
// stream needs so it can filter pushes per account.
func (uc *WebmailUseCase) MailboxIDFor(ctx context.Context, address string) (string, error) {
	return uc.mailboxID(ctx, address)
}

func (uc *WebmailUseCase) mailboxID(ctx context.Context, address string) (string, error) {
	id, err := uc.repo.FindMailboxIDByAddress(ctx, address)
	if err != nil || id == "" {
		return "", ErrNoSuchMailbox
	}
	return id, nil
}

func (uc *WebmailUseCase) ListFolders(ctx context.Context, address string) ([]port.WebmailFolder, error) {
	id, err := uc.mailboxID(ctx, address)
	if err != nil {
		return nil, err
	}
	return uc.repo.ListFolders(ctx, id)
}

func (uc *WebmailUseCase) ListMessages(
	ctx context.Context, address, folder, search string, limit, offset int,
) ([]port.WebmailMessage, error) {
	id, err := uc.mailboxID(ctx, address)
	if err != nil {
		return nil, err
	}
	return uc.repo.ListMessages(ctx, id, folder, search, limit, offset)
}

// GetMessage returns the raw RFC 5322 bytes and marks the message read.
func (uc *WebmailUseCase) GetMessage(ctx context.Context, address, folder string, uid uint32) ([]byte, error) {
	id, err := uc.mailboxID(ctx, address)
	if err != nil {
		return nil, err
	}

	blobID, err := uc.repo.GetMessageBlob(ctx, id, folder, uid)
	if err != nil {
		return nil, err
	}

	raw, err := uc.blobs.ReadByID(ctx, blobID)
	if err != nil {
		return nil, err
	}

	// Opening a message marks it read, but a failure to record that must not
	// stop it being displayed.
	_ = uc.repo.MarkSeen(ctx, id, folder, uid, true)
	return raw, nil
}

func (uc *WebmailUseCase) SetSeen(ctx context.Context, address, folder string, uid uint32, seen bool) error {
	id, err := uc.mailboxID(ctx, address)
	if err != nil {
		return err
	}
	return uc.repo.MarkSeen(ctx, id, folder, uid, seen)
}

// ComposeInput is one message written in the webmail.
type ComposeInput struct {
	From string
	To   []string
	Cc   []string
	// Bcc reaches the envelope but never the headers - that is the whole
	// point of it. It used not to exist here at all, so a blind copy typed
	// into the webmail was silently dropped and nobody received it.
	Bcc     []string
	Subject string
	// Text is the plain-text body. HTML, when present, is what the composer
	// produced; both are sent together as multipart/alternative so a reader
	// that cannot or will not render HTML still has something to show.
	Text string
	HTML string
	// DraftUID is the autosaved draft this message grew out of, or 0 when it
	// was composed without one ever being stored.
	//
	// Sending used to ignore it entirely, so a message that had been autosaved
	// went out and its draft stayed in Drafts: a duplicate of mail already on
	// its way, which no screen in the webmail could then delete.
	DraftUID uint32
}

// ErrNoRecipients rejects a submission with nobody to deliver to, before it
// reaches the queue.
var ErrNoRecipients = errors.New("webmail: at least one recipient is required")

// Send builds an RFC 5322 message and hands it to the same submission use
// case the SMTP ports use, so webmail mail gets the identical treatment:
// send limits, DKIM signing and the durable queue.
func (uc *WebmailUseCase) Send(ctx context.Context, input ComposeInput) error {
	// Bcc joins the envelope here and is left out of buildMimeMessage, which
	// is what makes it blind.
	recipients := normaliseRecipients(
		append(append(append([]string{}, input.To...), input.Cc...), input.Bcc...))
	if len(recipients) == 0 {
		return ErrNoRecipients
	}

	account, err := uc.auth.FindByAddress(ctx, input.From)
	if err != nil {
		return err
	}
	if account == nil {
		return ErrNoSuchMailbox
	}

	payload := buildMimeMessage(input, recipients, uc.localHost)

	if err := uc.submission.Submit(ctx, ProcessOutboundEmailInput{
		MailboxID:            account.ID,
		SenderAddr:           account.EmailAddress,
		DomainName:           account.DomainName,
		Recipients:           recipients,
		Body:                 bytes.NewReader(payload),
		MaxRecipientsPerHour: account.MaxRecipientsPerHour,
	}); err != nil {
		return err
	}

	// Only after the message is durably queued. Filing it first would leave a
	// copy in Sent for a message that was then rejected by the send limit.
	//
	// A failure here is logged and swallowed: the mail is already on its way,
	// and telling the composer the send failed would invite a duplicate.
	if err := uc.fileLocalCopy(ctx, account.ID, account.EmailAddress, "Sent", payload); err != nil {
		log.Printf("webmail: could not file the Sent copy for %s: %v", account.EmailAddress, err)
	}

	// The draft this message grew out of has been superseded by the real
	// thing. Swallowed like the Sent copy above and for the same reason: the
	// mail is already queued, and failing the request now would only invite a
	// second copy to be sent.
	if input.DraftUID != 0 {
		if mailboxID, idErr := uc.mailboxID(ctx, account.EmailAddress); idErr == nil {
			if err := uc.repo.Expunge(ctx, mailboxID, "Drafts", input.DraftUID); err != nil {
				log.Printf("webmail: could not discard the sent draft %d: %v", input.DraftUID, err)
			}
		}
	}
	return nil
}

// Delete removes one message the way a mail client does: from any ordinary
// folder it goes to Trash, and from Trash it is really expunged.
//
// The webmail had no delete at all. Expunge existed but was reachable only
// when an autosave superseded a draft, so anything the user wanted rid of -
// most visibly the draft left behind by a sent message - stayed put.
func (uc *WebmailUseCase) Delete(ctx context.Context, address, folder string, uid uint32) error {
	mailboxID, err := uc.mailboxID(ctx, address)
	if err != nil {
		return err
	}

	_, err = uc.repo.MoveToTrash(ctx, mailboxID, folder, uid)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, port.ErrAlreadyInTrash), errors.Is(err, port.ErrNoTrashFolder):
		// Already in Trash, or nowhere to move it to. Either way the second
		// press is the one that removes it for good.
		return uc.repo.Expunge(ctx, mailboxID, folder, uid)
	default:
		return err
	}
}

// fileLocalCopy stores a message the user themselves composed into one of
// their own folders. It reuses the inbound persistence path so the copy gets a
// real IMAP UID and shows up over IMAP as well as in the webmail.
func (uc *WebmailUseCase) fileLocalCopy(
	ctx context.Context, mailboxID uuid.UUID, address, folder string, payload []byte,
) error {
	if uc.blobStore == nil || uc.messages == nil {
		return nil
	}
	blob, err := uc.blobStore.Store(ctx, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	headers := ExtractMessageHeaders(payload)
	_, err = uc.messages.PersistAll(ctx, []port.PersistInboundMessageInput{{
		MailboxID:        mailboxID,
		Blob:             blob,
		SenderAddress:    address,
		RecipientAddress: address,
		TargetFolderName: folder,
		Subject:          headers.Subject,
		Snippet:          headers.Snippet,
		FromDisplayName:  headers.FromDisplayName,
		MessageIDHeader:  headers.MessageID,
		HasAttachments:   headers.HasAttachments,
		AlreadySeen:      true,
	}})
	return err
}

// SaveDraft files the message being composed into the Drafts folder.
//
// Nothing did this before: the composer displayed "draft saved automatically"
// from a setTimeout, with no request behind it, and closing the tab lost the
// message. Drafts are whole RFC 5322 messages like any other, so they are
// readable over IMAP too.
// replaceUID is the draft this one supersedes, or 0 for the first save of a
// message. Without it every autosave would append another copy and a few
// minutes of typing would leave the Drafts folder full of the same half-written
// message.
func (uc *WebmailUseCase) SaveDraft(ctx context.Context, input ComposeInput, replaceUID uint32) (uint32, error) {
	account, err := uc.auth.FindByAddress(ctx, input.From)
	if err != nil {
		return 0, err
	}
	if account == nil {
		return 0, ErrNoSuchMailbox
	}
	if uc.blobStore == nil || uc.messages == nil {
		return 0, ErrDraftsUnavailable
	}

	// A draft has no envelope, so recipients may legitimately be empty here -
	// unlike Send, this must not reject a half-written message.
	recipients := normaliseRecipients(
		append(append(append([]string{}, input.To...), input.Cc...), input.Bcc...))
	payload := buildMimeMessage(input, recipients, uc.localHost)

	blob, err := uc.blobStore.Store(ctx, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	headers := ExtractMessageHeaders(payload)
	uids, err := uc.messages.PersistAll(ctx, []port.PersistInboundMessageInput{{
		MailboxID:        account.ID,
		Blob:             blob,
		SenderAddress:    account.EmailAddress,
		RecipientAddress: account.EmailAddress,
		TargetFolderName: "Drafts",
		Subject:          headers.Subject,
		Snippet:          headers.Snippet,
		FromDisplayName:  headers.FromDisplayName,
		MessageIDHeader:  headers.MessageID,
		HasAttachments:   headers.HasAttachments,
		AlreadySeen:      true,
	}})
	if err != nil {
		return 0, err
	}

	// Only after the replacement is committed, so a failure here leaves the
	// older draft in place rather than losing both.
	if replaceUID != 0 {
		mailboxID, idErr := uc.mailboxID(ctx, account.EmailAddress)
		if idErr == nil {
			if err := uc.repo.Expunge(ctx, mailboxID, "Drafts", replaceUID); err != nil {
				log.Printf("webmail: could not remove the superseded draft %d: %v", replaceUID, err)
			}
		}
	}

	if len(uids) == 0 {
		return 0, nil
	}
	return uint32(uids[0]), nil
}

// ErrDraftsUnavailable reports that this deployment cannot store drafts,
// rather than accepting one and dropping it.
var ErrDraftsUnavailable = errors.New("webmail: draft storage is not configured")

func normaliseRecipients(raw []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, entry := range raw {
		for _, part := range strings.FieldsFunc(entry, func(r rune) bool { return r == ',' || r == ';' }) {
			addr, err := mail.ParseAddress(strings.TrimSpace(part))
			if err != nil {
				continue
			}
			if !seen[addr.Address] {
				seen[addr.Address] = true
				out = append(out, addr.Address)
			}
		}
	}
	return out
}

// buildMimeMessage assembles the headers a receiver expects. Date and
// Message-ID are set here because a message without them is scored as spam by
// most receivers (PLAN.md section 5).
//
// Bcc recipients are deliberately absent from the headers: they are carried in
// the envelope only, which is what "blind" means.
//
// A message with HTML goes out as multipart/alternative with a plain-text part
// first, in ascending order of preference per RFC 2046 section 5.1.4. Sending
// the composer's HTML under a text/plain header - which is what this did while
// the body was being dropped anyway - shows the recipient raw markup.
func buildMimeMessage(input ComposeInput, recipients []string, localHost string) []byte {
	var b strings.Builder
	b.WriteString("From: " + input.From + "\r\n")
	if len(input.To) > 0 {
		b.WriteString("To: " + strings.Join(input.To, ", ") + "\r\n")
	} else {
		to := normaliseRecipients(input.To)
		if len(to) == 0 {
			to = recipients
		}
		b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	}
	if len(input.Cc) > 0 {
		b.WriteString("Cc: " + strings.Join(input.Cc, ", ") + "\r\n")
	}
	b.WriteString("Subject: " + encodeHeaderValue(input.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString(fmt.Sprintf("Message-ID: <%s@%s>\r\n", uuid.NewString(), localHost))
	b.WriteString("MIME-Version: 1.0\r\n")

	text := input.Text
	if text == "" && input.HTML != "" {
		text = htmlToPlainText(input.HTML)
	}

	if input.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
		b.WriteString(normaliseEOL(text))
		return []byte(b.String())
	}

	boundary := "=_alt_" + uuid.NewString()
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(normaliseEOL(text))

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(normaliseEOL(input.HTML))

	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// normaliseEOL converts to CRLF and guarantees the trailing break a MIME part
// boundary must be preceded by.
func normaliseEOL(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
	if !strings.HasSuffix(s, "\r\n") {
		s += "\r\n"
	}
	return s
}

// htmlToPlainText derives a readable fallback from composed HTML.
//
// Deliberately crude - block elements become line breaks, tags are dropped,
// the handful of entities a contenteditable actually emits are decoded. It
// exists so the text/plain part is not empty, which is what makes a
// multipart/alternative message look like spam to a filter.
func htmlToPlainText(html string) string {
	replacer := strings.NewReplacer(
		"<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"</p>", "\n\n", "</div>", "\n", "</li>", "\n", "</tr>", "\n",
		"<li>", "- ",
	)
	s := replacer.Replace(html)

	var out strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			out.WriteRune(r)
		}
	}

	text := strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'",
	).Replace(out.String())

	// Collapse the runs of blank lines the block replacements leave behind.
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}

// encodeHeaderValue RFC 2047-encodes a subject that is not plain ASCII, so an
// accented subject does not arrive as mojibake.
func encodeHeaderValue(value string) string {
	for i := 0; i < len(value); i++ {
		if value[i] > 127 {
			return mimeEncode(value)
		}
	}
	// A bare CR or LF here would let the caller inject extra headers.
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

// mimeEncode wraps a non-ASCII header value as an RFC 2047 encoded word.
func mimeEncode(value string) string {
	clean := strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
	return mime.BEncoding.Encode("utf-8", clean)
}
