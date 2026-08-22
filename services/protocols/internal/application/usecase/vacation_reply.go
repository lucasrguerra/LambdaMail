package usecase

import (
	"bytes"
	"context"
	"fmt"
	"log"
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
		}
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
func buildVacationMessage(reply pendingVacation, host string) []byte {
	if host == "" {
		host = "localhost"
	}
	subject := reply.Subject
	if strings.TrimSpace(subject) == "" {
		subject = "Out of office"
	}

	var b strings.Builder
	b.WriteString("From: " + reply.From + "\r\n")
	b.WriteString("To: " + reply.To + "\r\n")
	b.WriteString("Subject: " + encodeHeaderValue(subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString(fmt.Sprintf("Message-ID: <%s@%s>\r\n", uuid.NewString(), host))
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
