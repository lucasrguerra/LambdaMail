package usecase

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

type stubAuthenticator struct {
	result port.InboundAuthResult
	seen   port.InboundAuthInput
}

func (s *stubAuthenticator) Authenticate(_ context.Context, input port.InboundAuthInput) port.InboundAuthResult {
	s.seen = input
	return s.result
}

func inboundInput(recipients int) ProcessInboundEmailInput {
	in := ProcessInboundEmailInput{
		Sender: "sender@sender.test",
		Body:   bytes.NewBufferString("From: sender@sender.test\r\n\r\nbody"),
	}
	for i := 0; i < recipients; i++ {
		in.Recipients = append(in.Recipients, port.MailboxRecord{ID: uuid.New()})
		in.RecipientAddresses = append(in.RecipientAddresses, "rcpt@example.test")
	}
	return in
}

// The verdicts must reach the database, otherwise the webmail and the DMARC
// dashboard have nothing to show (PLAN.md section 6.1).
func TestUseCase_Handle_PersistsAuthenticationVerdicts(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetAuthenticator(&stubAuthenticator{result: port.InboundAuthResult{
		SPF: port.AuthResultPass, DKIM: port.AuthResultPass, DMARC: port.AuthResultPass,
	}})

	if err := uc.Handle(context.Background(), inboundInput(1)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := messages.persisted[0]
	if got.SPFResult != "pass" || got.DKIMResult != "pass" || got.DMARCResult != "pass" {
		t.Errorf("stored verdicts = %q/%q/%q, want pass/pass/pass", got.SPFResult, got.DKIMResult, got.DMARCResult)
	}
}

// RFC 8601: our own checks must be recorded in the delivered message so the
// spam filter and the user can see them.
func TestUseCase_Handle_PrependsAuthenticationResultsHeader(t *testing.T) {
	blobs := &capturingBlobStorage{}
	uc := NewProcessInboundEmailUseCase(nil, blobs, &recordingMessageRepository{})
	uc.SetAuthenticator(&stubAuthenticator{result: port.InboundAuthResult{
		SPF: port.AuthResultPass, DKIM: port.AuthResultNone, DMARC: port.AuthResultPass,
		AuthenticationResults: "mail.lambdamail.test; spf=pass; dmarc=pass",
	}})

	if err := uc.Handle(context.Background(), inboundInput(1)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if !strings.HasPrefix(string(blobs.stored), "Authentication-Results: mail.lambdamail.test") {
		t.Errorf("stored message does not lead with Authentication-Results:\n%s", blobs.stored)
	}
}

// A sender that published p=reject asked for exactly this. Accepting the
// message instead would defeat the policy the sender is paying for.
func TestUseCase_Handle_RejectsOnDmarcFailWithRejectPolicy(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetAuthenticator(&stubAuthenticator{result: port.InboundAuthResult{
		SPF: port.AuthResultFail, DKIM: port.AuthResultNone,
		DMARC: port.AuthResultFail, DmarcPolicy: "reject",
	}})

	err := uc.Handle(context.Background(), inboundInput(1))
	if err == nil {
		t.Fatal("expected the message to be refused")
	}
	if !strings.HasPrefix(err.Error(), "550 ") {
		t.Errorf("error = %q, want a permanent 550 rejection", err)
	}
	if len(messages.persisted) != 0 {
		t.Error("a DMARC-rejected message was stored anyway")
	}
}

// Quarantine is a filing decision, not a refusal: the message is accepted and
// the spam filter decides where it lands.
func TestUseCase_Handle_AcceptsOnDmarcFailWithQuarantinePolicy(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetAuthenticator(&stubAuthenticator{result: port.InboundAuthResult{
		SPF: port.AuthResultFail, DMARC: port.AuthResultFail, DmarcPolicy: "quarantine",
	}})

	if err := uc.Handle(context.Background(), inboundInput(1)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(messages.persisted) != 1 {
		t.Errorf("persisted %d messages, want 1", len(messages.persisted))
	}
}

// SPF is defined over the peer address and the HELO name, so both have to
// reach the authenticator.
func TestUseCase_Handle_PassesPeerIdentityToAuthenticator(t *testing.T) {
	auth := &stubAuthenticator{result: port.InboundAuthResult{SPF: port.AuthResultNone}}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, &recordingMessageRepository{})
	uc.SetAuthenticator(auth)

	in := inboundInput(1)
	in.ClientIP = parseTestIP("192.0.2.10")
	in.HeloDomain = "mx.sender.test"

	if err := uc.Handle(context.Background(), in); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if auth.seen.ClientIP.String() != "192.0.2.10" {
		t.Errorf("ClientIP = %v, want 192.0.2.10", auth.seen.ClientIP)
	}
	if auth.seen.HeloDomain != "mx.sender.test" {
		t.Errorf("HeloDomain = %q, want mx.sender.test", auth.seen.HeloDomain)
	}
	if auth.seen.EnvelopeFrom != "sender@sender.test" {
		t.Errorf("EnvelopeFrom = %q", auth.seen.EnvelopeFrom)
	}
}

func parseTestIP(s string) net.IP { return net.ParseIP(s) }
