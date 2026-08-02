package smtppresentation

import (
	"context"
	"errors"
	"io"

	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

// SubmissionBackend serves the submission ports (587 and 465). It is a
// separate backend from the MX one on purpose: submission always requires
// authentication and always relays outward, while port 25 never authenticates
// and only accepts mail for local mailboxes (PLAN.md section 4).
type SubmissionBackend struct {
	useCase *usecase.ProcessOutboundEmailUseCase
}

func NewSubmissionBackend(uc *usecase.ProcessOutboundEmailUseCase) *SubmissionBackend {
	return &SubmissionBackend{useCase: uc}
}

func (b *SubmissionBackend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &submissionSession{useCase: b.useCase}, nil
}

type submissionSession struct {
	useCase *usecase.ProcessOutboundEmailUseCase
	// authenticated is nil until AUTH succeeds; every other command checks it.
	authenticated *port.MailboxAuth
	from          string
	recipients    []string
}

// AuthMechanisms advertises PLAIN and LOGIN. The server only offers them once
// the connection is encrypted - go-smtp withholds AUTH before TLS unless
// AllowInsecureAuth is set, which this deployment never sets (PLAN.md
// section 4: credentials never travel in the clear).
func (s *submissionSession) AuthMechanisms() []string {
	return []string{sasl.Plain, loginMechanism}
}

func (s *submissionSession) Auth(mech string) (sasl.Server, error) {
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			return s.authenticate(username, password)
		}), nil
	case loginMechanism:
		return newLoginServer(s.authenticate), nil
	default:
		return nil, gosmtp.ErrAuthUnsupported
	}
}

func (s *submissionSession) authenticate(username, password string) error {
	rec, err := s.useCase.Authenticate(context.Background(), username, password)
	if err != nil {
		return &gosmtp.SMTPError{
			Code:         535,
			EnhancedCode: gosmtp.EnhancedCode{5, 7, 8},
			Message:      "Authentication credentials invalid",
		}
	}
	s.authenticated = rec
	return nil
}

func (s *submissionSession) Reset() {
	s.from = ""
	s.recipients = nil
}

func (s *submissionSession) Logout() error {
	s.authenticated = nil
	return nil
}

func (s *submissionSession) Mail(from string, _ *gosmtp.MailOptions) error {
	if s.authenticated == nil {
		return errAuthRequired()
	}
	if err := usecase.AuthorizeSender(s.authenticated, from); err != nil {
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 7, 1},
			Message:      "Sender address not owned by the authenticated account",
		}
	}
	s.from = from
	return nil
}

func (s *submissionSession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	if s.authenticated == nil {
		return errAuthRequired()
	}
	s.recipients = append(s.recipients, to)
	return nil
}

func (s *submissionSession) Data(r io.Reader) error {
	if s.authenticated == nil {
		return errAuthRequired()
	}
	if len(s.recipients) == 0 {
		return &gosmtp.SMTPError{
			Code:         503,
			EnhancedCode: gosmtp.EnhancedCode{5, 5, 1},
			Message:      "RCPT TO required before DATA",
		}
	}

	err := s.useCase.Submit(context.Background(), usecase.ProcessOutboundEmailInput{
		MailboxID:            s.authenticated.ID,
		SenderAddr:           s.from,
		DomainName:           s.authenticated.DomainName,
		Recipients:           s.recipients,
		Body:                 r,
		MaxRecipientsPerHour: s.authenticated.MaxRecipientsPerHour,
	})
	if err == nil {
		return nil
	}

	// A rate-limited sender gets a 4xx: the message is legitimate, the
	// account has simply run out of budget for this hour and may retry.
	if errors.Is(err, usecase.ErrSendLimitExceeded) {
		return &gosmtp.SMTPError{
			Code:         451,
			EnhancedCode: gosmtp.EnhancedCode{4, 7, 0},
			Message:      "Hourly recipient limit reached, try again later",
		}
	}

	return &gosmtp.SMTPError{
		Code:         451,
		EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
		Message:      "Temporary error accepting message for delivery",
	}
}

func errAuthRequired() error {
	return &gosmtp.SMTPError{
		Code:         530,
		EnhancedCode: gosmtp.EnhancedCode{5, 7, 0},
		Message:      "Authentication required",
	}
}

// Ensure the session satisfies both the base and the auth add-on interface.
var (
	_ gosmtp.Session     = (*submissionSession)(nil)
	_ gosmtp.AuthSession = (*submissionSession)(nil)
)
