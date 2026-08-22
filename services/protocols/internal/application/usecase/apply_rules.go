package usecase

import (
	"bytes"
	"context"
	"log"
	"net/mail"
	"strings"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/sieve"
)

// ApplyRulesUseCase runs a mailbox's own rules over an arriving message.
//
// Nothing did this. The scripts were stored by ManageSieve and written by the
// settings screen, and the delivery path never read either - so a vacation
// responder that was switched on never replied, and a filter that said "file
// this in Faturas" filed nothing, ever.
type ApplyRulesUseCase struct {
	scripts port.SieveScriptReader
}

func NewApplyRulesUseCase(scripts port.SieveScriptReader) *ApplyRulesUseCase {
	return &ApplyRulesUseCase{scripts: scripts}
}

// RuleDecision is what the mailbox's rules decided about one message.
type RuleDecision struct {
	// Folder overrides where the message is filed, or is empty to leave the
	// delivery path's own choice alone.
	Folder string
	// Discard drops the message after it has been accepted. The sender is
	// still told the message was delivered, because a filing rule is not a
	// rejection - telling them otherwise would have their server retry.
	Discard bool
	Flags   []string
	// Vacation is the automatic reply to send, or nil.
	Vacation *sieve.VacationReply
}

// For evaluates the receiving mailbox's rules against one message.
//
// It never returns an error, and that is deliberate. Filing is a convenience;
// delivery is not. A script that does not parse, or a database that cannot be
// read, must leave the message delivered unfiltered rather than bouncing mail
// that the sender would then retry forever.
func (uc *ApplyRulesUseCase) For(
	ctx context.Context, mailboxID uuid.UUID, recipient, sender string, payload []byte,
) RuleDecision {
	if uc == nil || uc.scripts == nil {
		return RuleDecision{}
	}

	source, err := uc.scripts.ActiveScriptFor(ctx, mailboxID)
	if err != nil {
		log.Printf("rules: could not read the rules for %s, delivering unfiltered: %v", recipient, err)
		return RuleDecision{}
	}
	if strings.TrimSpace(source) == "" {
		return RuleDecision{}
	}

	script, err := sieve.Parse(source)
	if err != nil {
		// Logged rather than swallowed silently: the user wrote a rule that
		// cannot run, and that is worth knowing about.
		log.Printf("rules: the rules for %s could not be read, delivering unfiltered: %v", recipient, err)
		return RuleDecision{}
	}

	outcome := sieve.Evaluate(script, sieve.Message{
		Headers:   headersOf(payload),
		Sender:    sender,
		Recipient: recipient,
	})

	return RuleDecision{
		Folder:   outcome.Folder,
		Discard:  outcome.Discard,
		Flags:    outcome.Flags,
		Vacation: outcome.Vacation,
	}
}

// headersOf reads the message's headers, keyed lowercased.
//
// A message whose headers cannot be parsed still gets evaluated, against an
// empty set: a rule simply will not match, which is the harmless outcome.
func headersOf(payload []byte) map[string][]string {
	parsed, err := mail.ReadMessage(bytes.NewReader(payload))
	if err != nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(parsed.Header))
	for name, values := range parsed.Header {
		out[strings.ToLower(name)] = values
	}
	return out
}
