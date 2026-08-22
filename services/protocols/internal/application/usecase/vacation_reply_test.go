package usecase

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// The out-of-office reply has to arrive inside the conversation it answers.
//
// It was sent with no threading headers at all, so every mail client showed it
// as a brand-new conversation with no relation to the message it was replying
// to - the sender saw a loose "Fora do escritorio" message and had to work out
// for themselves which of their mails had triggered it.
//
// RFC 5322 section 3.6.4 is what threads a reply: In-Reply-To names the
// message being answered, and References carries the whole chain so a client
// can place it even when part of the thread is missing.

func vacationHeaders(t *testing.T, reply pendingVacation) map[string]string {
	t.Helper()
	payload := string(buildVacationMessage(reply, "mail.example.test"))
	head, _, _ := strings.Cut(payload, "\r\n\r\n")

	out := map[string]string{}
	for _, line := range strings.Split(head, "\r\n") {
		if name, value, found := strings.Cut(line, ":"); found {
			out[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
		}
	}
	return out
}

func replyTo(original, references string) pendingVacation {
	return pendingVacation{
		To: "amigo@example.test", From: "me@example.test",
		Subject: "Fora do escritorio", Body: "Volto dia 30.",
		OriginalMessageID:  original,
		OriginalReferences: references,
		OriginalSubject:    "Proposta comercial",
	}
}

func TestReplyNamesTheMessageItAnswers(t *testing.T) {
	headers := vacationHeaders(t, replyTo("<abc123@example.test>", ""))

	if headers["in-reply-to"] != "<abc123@example.test>" {
		t.Errorf("In-Reply-To is %q, want the original Message-ID", headers["in-reply-to"])
	}
}

// References carries the chain, so the reply lands in the right place even in
// a client that never saw the intervening messages.
func TestReplyCarriesTheConversationChain(t *testing.T) {
	headers := vacationHeaders(t, replyTo("<c@example.test>", "<a@example.test> <b@example.test>"))

	refs := headers["references"]
	for _, want := range []string{"<a@example.test>", "<b@example.test>", "<c@example.test>"} {
		if !strings.Contains(refs, want) {
			t.Errorf("References %q is missing %s", refs, want)
		}
	}
	// The message being answered goes last, which is the order a client reads
	// the chain in.
	if !strings.HasSuffix(refs, "<c@example.test>") {
		t.Errorf("References %q should end with the message being answered", refs)
	}
}

// A first message in a conversation has no References of its own; the reply
// starts the chain with it.
func TestReplyStartsTheChainWhenThereIsNone(t *testing.T) {
	headers := vacationHeaders(t, replyTo("<only@example.test>", ""))

	if headers["references"] != "<only@example.test>" {
		t.Errorf("References is %q, want just the original", headers["references"])
	}
}

// Some senders omit Message-ID. Without one there is nothing to thread on, and
// inventing a reference to a message that does not exist would have clients
// show the reply attached to nothing.
func TestReplyOmitsThreadingWhenThereIsNothingToThreadOn(t *testing.T) {
	headers := vacationHeaders(t, replyTo("", ""))

	if _, present := headers["in-reply-to"]; present {
		t.Error("In-Reply-To was written with no original Message-ID")
	}
	if _, present := headers["references"]; present {
		t.Error("References was written with no original Message-ID")
	}
}

// The subject is what a person sees in their list, and a reply that keeps the
// original subject reads as part of the conversation. The configured text
// stays, as the prefix rather than as a replacement, so the reader still knows
// it is an out-of-office note.
func TestSubjectShowsItIsAReplyToTheirMessage(t *testing.T) {
	headers := vacationHeaders(t, replyTo("<abc@example.test>", ""))

	subject := headers["subject"]
	if !strings.Contains(subject, "Proposta comercial") {
		t.Errorf("subject %q does not mention the message being answered", subject)
	}
	if !strings.Contains(subject, "Fora do escritorio") {
		t.Errorf("subject %q dropped the configured text", subject)
	}
}

// A subject that already says Re: must not collect a second one.
func TestSubjectDoesNotStackReplyPrefixes(t *testing.T) {
	reply := replyTo("<abc@example.test>", "")
	reply.OriginalSubject = "Re: Proposta comercial"
	subject := vacationHeaders(t, reply)["subject"]

	if strings.Count(strings.ToLower(subject), "re:") > 1 {
		t.Errorf("subject %q stacked Re: prefixes", subject)
	}
}

// With no original subject there is nothing to echo, and the configured text
// stands on its own.
func TestSubjectFallsBackToTheConfiguredText(t *testing.T) {
	reply := replyTo("<abc@example.test>", "")
	reply.OriginalSubject = ""

	if got := vacationHeaders(t, reply)["subject"]; !strings.Contains(got, "Fora do escritorio") {
		t.Errorf("subject %q", got)
	}
}

// The headers that stop two autoresponders answering each other must survive
// all of this.
func TestReplyStillMarksItselfAutomatic(t *testing.T) {
	headers := vacationHeaders(t, replyTo("<abc@example.test>", ""))

	if headers["auto-submitted"] != "auto-replied" {
		t.Errorf("Auto-Submitted is %q", headers["auto-submitted"])
	}
	if headers["x-auto-response-suppress"] == "" {
		t.Error("X-Auto-Response-Suppress is missing")
	}
}

// A Message-ID arriving without its angle brackets is still usable; a
// reference without them is not valid in the header.
func TestMalformedMessageIdIsBracketed(t *testing.T) {
	headers := vacationHeaders(t, replyTo("abc123@example.test", ""))

	if headers["in-reply-to"] != "<abc123@example.test>" {
		t.Errorf("In-Reply-To is %q, want it bracketed", headers["in-reply-to"])
	}
}

// A header value cannot carry a newline: one would end the header and let the
// rest be read as more headers.
func TestThreadingHeadersCannotInjectMoreHeaders(t *testing.T) {
	headers := vacationHeaders(t, replyTo("<a@b.test>\r\nBcc: victim@example.test", ""))

	if _, present := headers["bcc"]; present {
		t.Error("a header was injected through the Message-ID")
	}
}

// --- end to end through the delivery path --------------------------------

// The whole path: a message arrives, the rules produce a reply, and the reply
// that leaves names the message that triggered it.
func TestDeliveredMessageProducesAThreadedReply(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetRules(newRules(&stubSieveScripts{script: vacationRule}))

	// No submission path configured, so the reply is assembled and logged
	// rather than queued - which is enough to check what it would carry.
	payload := "From: amigo@example.test\r\n" +
		"Subject: Proposta comercial\r\n" +
		"Message-ID: <original-123@example.test>\r\n" +
		"References: <first@example.test>\r\n" +
		"\r\ncorpo"

	if err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "amigo@example.test",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"me@example.test"},
		Body:               bytesReader(payload),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// The rules ran and produced a reply carrying the original's identity.
	decision := newRules(&stubSieveScripts{script: vacationRule}).For(
		context.Background(), uuid.New(), "me@example.test", "amigo@example.test", []byte(payload))
	if decision.Vacation == nil {
		t.Fatal("no reply was produced")
	}

	built := string(buildVacationMessage(pendingVacation{
		To: "amigo@example.test", From: "me@example.test",
		Subject: decision.Vacation.Subject, Body: decision.Vacation.Body,
		OriginalMessageID:  "<original-123@example.test>",
		OriginalReferences: "<first@example.test>",
		OriginalSubject:    "Proposta comercial",
	}, "mail.example.test"))

	for _, want := range []string{
		"In-Reply-To: <original-123@example.test>",
		"References: <first@example.test> <original-123@example.test>",
		"Proposta comercial",
		"Auto-Submitted: auto-replied",
	} {
		if !strings.Contains(built, want) {
			t.Errorf("the reply is missing %q:\n%s", want, built)
		}
	}
}
