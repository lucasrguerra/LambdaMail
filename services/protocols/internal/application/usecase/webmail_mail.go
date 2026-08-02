package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	From    string
	To      []string
	Cc      []string
	Subject string
	Body    string
}

// ErrNoRecipients rejects a submission with nobody to deliver to, before it
// reaches the queue.
var ErrNoRecipients = errors.New("webmail: at least one recipient is required")

// Send builds an RFC 5322 message and hands it to the same submission use
// case the SMTP ports use, so webmail mail gets the identical treatment:
// send limits, DKIM signing and the durable queue.
func (uc *WebmailUseCase) Send(ctx context.Context, input ComposeInput) error {
	recipients := normaliseRecipients(append(append([]string{}, input.To...), input.Cc...))
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

	return uc.submission.Submit(ctx, ProcessOutboundEmailInput{
		MailboxID:            account.ID,
		SenderAddr:           account.EmailAddress,
		DomainName:           account.DomainName,
		Recipients:           recipients,
		Body:                 bytes.NewReader(payload),
		MaxRecipientsPerHour: account.MaxRecipientsPerHour,
	})
}

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
func buildMimeMessage(input ComposeInput, recipients []string, localHost string) []byte {
	var b strings.Builder
	b.WriteString("From: " + input.From + "\r\n")
	if len(input.To) > 0 {
		b.WriteString("To: " + strings.Join(input.To, ", ") + "\r\n")
	} else {
		b.WriteString("To: " + strings.Join(recipients, ", ") + "\r\n")
	}
	if len(input.Cc) > 0 {
		b.WriteString("Cc: " + strings.Join(input.Cc, ", ") + "\r\n")
	}
	b.WriteString("Subject: " + encodeHeaderValue(input.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString(fmt.Sprintf("Message-ID: <%s@%s>\r\n", uuid.NewString(), localHost))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(input.Body, "\n", "\r\n"))
	if !strings.HasSuffix(input.Body, "\n") {
		b.WriteString("\r\n")
	}
	return []byte(b.String())
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
