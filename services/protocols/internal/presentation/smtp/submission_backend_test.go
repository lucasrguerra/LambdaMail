package smtppresentation

import (
	"strings"
	"testing"

	gosmtp "github.com/emersion/go-smtp"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

func smtpCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var smtpErr *gosmtp.SMTPError
	if !ok(err, &smtpErr) {
		t.Fatalf("error is not an SMTPError: %v", err)
	}
	return smtpErr.Code
}

func ok(err error, target **gosmtp.SMTPError) bool {
	e, is := err.(*gosmtp.SMTPError)
	if is {
		*target = e
	}
	return is
}

// Every command before AUTH must be refused: submission that accepted an
// unauthenticated MAIL FROM would be an open relay.
func TestSubmissionSession_RefusesCommandsBeforeAuth(t *testing.T) {
	s := &submissionSession{}

	if got := smtpCode(t, s.Mail("user@example.test", nil)); got != 530 {
		t.Errorf("MAIL FROM before AUTH = %d, want 530", got)
	}
	if got := smtpCode(t, s.Rcpt("rcpt@remote.test", nil)); got != 530 {
		t.Errorf("RCPT TO before AUTH = %d, want 530", got)
	}
	if got := smtpCode(t, s.Data(strings.NewReader("body"))); got != 530 {
		t.Errorf("DATA before AUTH = %d, want 530", got)
	}
}

// An authenticated account must not be able to send as anybody else.
func TestSubmissionSession_RefusesForeignSender(t *testing.T) {
	s := &submissionSession{authenticated: &port.MailboxAuth{EmailAddress: "user@example.test"}}

	if err := s.Mail("user@example.test", nil); err != nil {
		t.Fatalf("sending as itself was refused: %v", err)
	}
	if got := smtpCode(t, s.Mail("ceo@example.test", nil)); got != 550 {
		t.Errorf("MAIL FROM a foreign address = %d, want 550", got)
	}
}

func TestSubmissionSession_AdvertisesPlainAndLogin(t *testing.T) {
	mechs := (&submissionSession{}).AuthMechanisms()

	joined := strings.Join(mechs, " ")
	if !strings.Contains(joined, "PLAIN") || !strings.Contains(joined, "LOGIN") {
		t.Errorf("AuthMechanisms = %v, want PLAIN and LOGIN", mechs)
	}
}

func TestSubmissionSession_RejectsUnsupportedMechanism(t *testing.T) {
	if _, err := (&submissionSession{}).Auth("SCRAM-SHA-256"); err == nil {
		t.Fatal("expected an unsupported-mechanism error")
	}
}

// Reset must clear the envelope but keep the session authenticated: RSET
// starts a new transaction, it does not log the user out.
func TestSubmissionSession_ResetKeepsAuthentication(t *testing.T) {
	s := &submissionSession{authenticated: &port.MailboxAuth{EmailAddress: "user@example.test"}}
	s.Mail("user@example.test", nil)
	s.Rcpt("rcpt@remote.test", nil)

	s.Reset()

	if s.from != "" || len(s.recipients) != 0 {
		t.Errorf("envelope survived RSET: from=%q recipients=%v", s.from, s.recipients)
	}
	if s.authenticated == nil {
		t.Error("RSET logged the session out")
	}
}

// Compile-time proof that the use case wiring matches what the session needs.
var _ = usecase.NewProcessOutboundEmailUseCase
