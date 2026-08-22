package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// The rules, applied to mail as it is delivered.

type stubSieveScripts struct {
	script string
	err    error
	asked  []uuid.UUID
}

func (s *stubSieveScripts) ActiveScriptFor(_ context.Context, mailboxID uuid.UUID) (string, error) {
	s.asked = append(s.asked, mailboxID)
	return s.script, s.err
}

func newRules(scripts *stubSieveScripts) *ApplyRulesUseCase {
	return NewApplyRulesUseCase(scripts)
}

const filingRule = `if header :contains "Subject" "Fatura" { fileinto "Faturas"; }`

func TestRuleDecidesTheFolder(t *testing.T) {
	uc := newRules(&stubSieveScripts{script: filingRule})
	payload := []byte("From: banco@example.test\r\nSubject: Fatura de agosto\r\n\r\ncorpo")

	decision := uc.For(context.Background(), uuid.New(), "me@example.test", "banco@example.test", payload)
	if decision.Folder != "Faturas" {
		t.Errorf("folder %q, want Faturas", decision.Folder)
	}
}

func TestMessageWithNoMatchingRuleIsUntouched(t *testing.T) {
	uc := newRules(&stubSieveScripts{script: filingRule})
	payload := []byte("From: amigo@example.test\r\nSubject: Almoco\r\n\r\ncorpo")

	decision := uc.For(context.Background(), uuid.New(), "me@example.test", "amigo@example.test", payload)
	if decision.Folder != "" || decision.Discard {
		t.Errorf("an unmatched message was acted on: %+v", decision)
	}
}

// A mailbox with no rules is the common case and must cost nothing.
func TestNoScriptMeansNoDecision(t *testing.T) {
	uc := newRules(&stubSieveScripts{script: ""})
	decision := uc.For(context.Background(), uuid.New(), "me@example.test", "a@b.test",
		[]byte("Subject: x\r\n\r\ny"))
	if decision.Folder != "" || decision.Discard || decision.Vacation != nil {
		t.Errorf("a mailbox with no rules got a decision: %+v", decision)
	}
}

// The rules must never be able to stop mail arriving. A broken script, or a
// database that cannot be read, means the message is delivered unfiltered -
// not that it bounces.
func TestABrokenScriptStillDeliversTheMessage(t *testing.T) {
	uc := newRules(&stubSieveScripts{script: `if header :contains "Subject" {{{`})
	decision := uc.For(context.Background(), uuid.New(), "me@example.test", "a@b.test",
		[]byte("Subject: x\r\n\r\ny"))
	if decision.Folder != "" || decision.Discard {
		t.Errorf("a broken script changed the delivery: %+v", decision)
	}
}

func TestAFailingLookupStillDeliversTheMessage(t *testing.T) {
	uc := newRules(&stubSieveScripts{err: errors.New("database is down")})
	decision := uc.For(context.Background(), uuid.New(), "me@example.test", "a@b.test",
		[]byte("Subject: x\r\n\r\ny"))
	if decision.Folder != "" || decision.Discard {
		t.Errorf("an unreadable script changed the delivery: %+v", decision)
	}
}

// --- vacation ------------------------------------------------------------

const vacationRule = `require ["vacation"];
vacation :subject "Fora do escritorio" "Volto dia 30.";`

func TestVacationRepliesToAPerson(t *testing.T) {
	uc := newRules(&stubSieveScripts{script: vacationRule})
	payload := []byte("From: amigo@example.test\r\nSubject: Oi\r\n\r\ncorpo")

	decision := uc.For(context.Background(), uuid.New(), "me@example.test", "amigo@example.test", payload)
	if decision.Vacation == nil {
		t.Fatal("no reply was produced")
	}
	if decision.Vacation.Subject != "Fora do escritorio" {
		t.Errorf("subject %q", decision.Vacation.Subject)
	}
}

// The header the message carries decides, not just the envelope: a mailing
// list is recognised by List-Id, which lives in the headers.
func TestVacationStaysSilentForAList(t *testing.T) {
	uc := newRules(&stubSieveScripts{script: vacationRule})
	payload := []byte("From: lista@example.test\r\nList-Id: <l.example.test>\r\nSubject: Oi\r\n\r\ncorpo")

	decision := uc.For(context.Background(), uuid.New(), "me@example.test", "lista@example.test", payload)
	if decision.Vacation != nil {
		t.Error("replied to a mailing list")
	}
}

func TestVacationStaysSilentForABounce(t *testing.T) {
	uc := newRules(&stubSieveScripts{script: vacationRule})
	payload := []byte("From: MAILER-DAEMON@example.test\r\nSubject: failed\r\n\r\ncorpo")

	// Empty envelope sender: what a bounce actually arrives with.
	decision := uc.For(context.Background(), uuid.New(), "me@example.test", "", payload)
	if decision.Vacation != nil {
		t.Error("replied to a bounce")
	}
}

// The rules belong to the mailbox being delivered to, not to the sender.
func TestRulesAreLookedUpForTheReceivingMailbox(t *testing.T) {
	scripts := &stubSieveScripts{script: filingRule}
	uc := newRules(scripts)
	mailboxID := uuid.New()

	uc.For(context.Background(), mailboxID, "me@example.test", "a@b.test",
		[]byte("Subject: Fatura\r\n\r\ny"))

	if len(scripts.asked) != 1 || scripts.asked[0] != mailboxID {
		t.Errorf("looked up %v, want the receiving mailbox %v", scripts.asked, mailboxID)
	}
}

var _ = port.MailboxRecord{}

// --- the delivery path ---------------------------------------------------

// The rule has to reach the message that is actually filed.
func TestDeliveryFilesWhereTheRuleSays(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetRules(newRules(&stubSieveScripts{script: filingRule}))

	if err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "banco@example.test",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"me@example.test"},
		Body:               bytesReader("From: banco@example.test\r\nSubject: Fatura de agosto\r\n\r\ncorpo"),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(messages.persisted) != 1 {
		t.Fatalf("want one filed copy, got %d", len(messages.persisted))
	}
	if messages.persisted[0].TargetFolderName != "Faturas" {
		t.Errorf("filed in %q, want Faturas", messages.persisted[0].TargetFolderName)
	}
}

// Each recipient gets their own rules: two mailboxes on this server receiving
// the same message may file it in different places.
func TestEachRecipientGetsTheirOwnRules(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetRules(newRules(&stubSieveScripts{script: filingRule}))

	if err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "banco@example.test",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}, {ID: uuid.New()}},
		RecipientAddresses: []string{"a@example.test", "b@example.test"},
		Body:               bytesReader("Subject: Fatura\r\n\r\ncorpo"),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages.persisted) != 2 {
		t.Fatalf("want two copies, got %d", len(messages.persisted))
	}
	for i, filed := range messages.persisted {
		if filed.TargetFolderName != "Faturas" {
			t.Errorf("copy %d filed in %q", i, filed.TargetFolderName)
		}
	}
}

// A discarded message is accepted and then dropped: the sender is told it was
// delivered, because a filing rule is not a rejection.
func TestDiscardAcceptsTheMessageAndFilesNothing(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetRules(newRules(&stubSieveScripts{
		script: `if header :contains "Subject" "spam" { discard; }`,
	}))

	if err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "spammer@example.test",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"me@example.test"},
		Body:               bytesReader("Subject: spam barato\r\n\r\ncorpo"),
	}); err != nil {
		t.Fatalf("a discarded message must still be accepted: %v", err)
	}
	if len(messages.persisted) != 0 {
		t.Errorf("a discarded message was filed: %d copies", len(messages.persisted))
	}
}

// Without rules configured, delivery behaves exactly as it did before.
func TestDeliveryIsUnchangedWithoutRules(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)

	if err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "banco@example.test",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"me@example.test"},
		Body:               bytesReader("Subject: Fatura\r\n\r\ncorpo"),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if messages.persisted[0].TargetFolderName == "Faturas" {
		t.Error("a rule ran with no rules configured")
	}
}

// bytesReader is the message body as the delivery path receives it.
func bytesReader(s string) io.Reader { return bytes.NewBufferString(s) }
