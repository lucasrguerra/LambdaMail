package usecase

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"mime"
	"strings"
	"time"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// pendingVacation is one automatic reply the rules asked for, held until the
// message that triggered it is safely stored.
type pendingVacation struct {
	To      string
	From    string
	Subject string
	Body    string
	// The message being answered. Without these the reply arrives as a
	// conversation of its own, and the sender has to work out for themselves
	// which of their messages produced it.
	OriginalMessageID  string
	OriginalReferences string
	OriginalSubject    string
	// MailboxID is whose responder this is, for the suppression log.
	MailboxID uuid.UUID
}

// SetVacationSender enables actually sending the out-of-office replies the
// rules produce. Without it a reply is logged and not sent, which is what
// happens on a deployment with no submission path configured.
func (uc *ProcessInboundEmailUseCase) SetVacationSender(
	submission *ProcessOutboundEmailUseCase, auth port.AuthRepository, host string,
) {
	uc.submission = submission
	uc.auth = auth
	uc.replyHost = host
}

// SetVacationSuppression limits how often one sender is told. Optional:
// without it every message produces a reply, which is what happened before.
func (uc *ProcessInboundEmailUseCase) SetVacationSuppression(s *VacationSuppression) {
	uc.suppression = s
}

// sendVacationReplies queues the replies the rules produced.
//
// Failures are logged and swallowed. The reply is a courtesy; the message that
// triggered it is already delivered, and returning an error here would tell
// the sender their mail failed when it did not.
func (uc *ProcessInboundEmailUseCase) sendVacationReplies(ctx context.Context, replies []pendingVacation) {
	if len(replies) == 0 {
		return
	}
	if uc.submission == nil || uc.auth == nil {
		for _, reply := range replies {
			log.Printf("vacation: no submission path configured, not replying to %s on behalf of %s",
				reply.To, reply.From)
		}
		return
	}

	for _, reply := range replies {
		if err := uc.sendVacationReply(ctx, reply); err != nil {
			log.Printf("vacation: could not reply to %s on behalf of %s: %v", reply.To, reply.From, err)
			continue
		}
		// Recorded only after the reply is really queued, so a send that
		// failed does not silence the next message from the same person.
		uc.suppression.Record(ctx, reply.MailboxID, reply.To)
	}
}

func (uc *ProcessInboundEmailUseCase) sendVacationReply(ctx context.Context, reply pendingVacation) error {
	account, err := uc.auth.FindByAddress(ctx, reply.From)
	if err != nil {
		return err
	}
	if account == nil {
		return fmt.Errorf("no mailbox for %s", reply.From)
	}

	payload := buildVacationMessage(reply, uc.replyHost)
	return uc.submission.Submit(ctx, ProcessOutboundEmailInput{
		MailboxID:            account.ID,
		SenderAddr:           account.EmailAddress,
		DomainName:           account.DomainName,
		Recipients:           []string{reply.To},
		Body:                 bytes.NewReader(payload),
		MaxRecipientsPerHour: account.MaxRecipientsPerHour,
	})
}

// buildVacationMessage assembles the reply.
//
// Auto-Submitted: auto-replied is what stops two servers answering each other
// forever: RFC 3834 requires it on an automatic reply, and this server's own
// responder refuses to answer anything carrying it. Without the header, two
// mailboxes both on holiday would reply to each other until one of them ran
// out of send quota.
//
// In-Reply-To and References are what make it a reply rather than a new
// message. RFC 5322 section 3.6.4: the first names the message being answered,
// the second carries the chain so a client can place the reply even when it
// never saw the messages in between. Without them every mail client showed the
// out-of-office note as an unrelated conversation.
func buildVacationMessage(reply pendingVacation, host string) []byte {
	if host == "" {
		host = "localhost"
	}

	var b strings.Builder
	b.WriteString("From: " + reply.From + "\r\n")
	b.WriteString("To: " + reply.To + "\r\n")
	b.WriteString("Subject: " + encodeHeaderValue(vacationSubject(reply)) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString(fmt.Sprintf("Message-ID: <%s@%s>\r\n", uuid.NewString(), host))

	// Only when there is a real message to point at. Referencing one that does
	// not exist would have clients show the reply attached to nothing.
	if original := messageIDRef(reply.OriginalMessageID); original != "" {
		b.WriteString("In-Reply-To: " + original + "\r\n")
		b.WriteString("References: " + buildReferences(reply.OriginalReferences, original) + "\r\n")
	}

	b.WriteString("Auto-Submitted: auto-replied\r\n")
	// Tells Exchange and Outlook not to generate their own automatic answer.
	b.WriteString("X-Auto-Response-Suppress: All\r\n")
	// Marks it as unimportant to anything that sorts by precedence.
	b.WriteString("Precedence: bulk\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(normaliseEOL(reply.Body))
	return []byte(b.String())
}

// vacationSubject is what the sender sees in their message list.
//
// The configured text is kept as a prefix rather than as the whole subject:
// echoing the original is what makes the reply read as part of that
// conversation, and clients that thread on the subject line need it too.
func vacationSubject(reply pendingVacation) string {
	configured := strings.TrimSpace(reply.Subject)
	if configured == "" {
		configured = "Out of office"
	}

	// Decoded first. A subject carrying an accent travels as an RFC 2047
	// encoded word, and pasting the raw header in put
	// "=?UTF-8?Q?Re=3A_Fora...?=" in front of a real person. It also defeated
	// the check below: encoded, every subject starts with "=?", so an existing
	// "Re:" went unrecognised and a second one was added on top.
	original := stripReplyPrefixes(decodeHeaderValue(reply.OriginalSubject))
	if original == "" {
		return configured
	}

	// Someone replying to the automatic message itself would otherwise get
	// their own out-of-office text quoted back at them as the conversation's
	// subject.
	if strings.EqualFold(original, configured) {
		return configured
	}
	return configured + ": Re: " + original
}

// decodeHeaderValue reads an RFC 2047 encoded word back into text.
//
// A header that is not encoded, or that this decoder cannot read, is returned
// as it stands: showing the raw value is better than showing nothing, and a
// subject is not worth failing a reply over.
func decodeHeaderValue(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(raw)
	if err != nil {
		return raw
	}
	return strings.TrimSpace(decoded)
}

// stripReplyPrefixes removes the Re: a thread accumulates.
//
// A conversation that has been round several times carries "Re: RE: Re:", and
// each one added another. One is enough, and it is added back by the caller.
func stripReplyPrefixes(subject string) string {
	out := strings.TrimSpace(subject)
	for {
		lower := strings.ToLower(out)
		switch {
		case strings.HasPrefix(lower, "re:"):
			out = strings.TrimSpace(out[3:])
		case strings.HasPrefix(lower, "fwd:"):
			out = strings.TrimSpace(out[4:])
		case strings.HasPrefix(lower, "fw:"):
			out = strings.TrimSpace(out[3:])
		default:
			return out
		}
	}
}

// messageIDRef normalises a Message-ID into something usable as a reference.
//
// A value carrying a line break would end the header and let whatever follows
// be read as further headers, so it is refused outright rather than trimmed:
// this value came from a remote sender.
func messageIDRef(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" || strings.ContainsAny(id, "\r\n") {
		return ""
	}
	if !strings.HasPrefix(id, "<") {
		id = "<" + id
	}
	if !strings.HasSuffix(id, ">") {
		id = id + ">"
	}
	return id
}

// buildReferences puts the answered message at the end of the chain, which is
// the order RFC 5322 defines and the order a client reads it in.
func buildReferences(existing, original string) string {
	chain := strings.Fields(strings.ReplaceAll(strings.TrimSpace(existing), "\r\n", " "))
	out := make([]string, 0, len(chain)+1)
	for _, ref := range chain {
		if ref != original && strings.HasPrefix(ref, "<") {
			out = append(out, ref)
		}
	}
	out = append(out, original)

	// Bounded: a long-running thread would otherwise grow this header without
	// limit. Keeping the first and the most recent is what RFC 5322 section
	// 3.6.4 suggests when a chain has to be trimmed.
	const maxRefs = 20
	if len(out) > maxRefs {
		trimmed := append([]string{out[0]}, out[len(out)-(maxRefs-1):]...)
		out = trimmed
	}
	return strings.Join(out, " ")
}
