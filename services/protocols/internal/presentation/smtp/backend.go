// Package smtppresentation adapts github.com/emersion/go-smtp's Backend/
// Session interfaces to ProcessInboundEmailUseCase (Clean Architecture
// layer 4 - PLAN.md section 3).
package smtppresentation

import (
	"context"
	"errors"
	"io"

	gosmtp "github.com/emersion/go-smtp"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

// Backend is the go-smtp entry point: one Session per connection.
type Backend struct {
	useCase *usecase.ProcessInboundEmailUseCase
}

func NewBackend(uc *usecase.ProcessInboundEmailUseCase) *Backend {
	return &Backend{useCase: uc}
}

func (b *Backend) NewSession(_ *gosmtp.Conn) (gosmtp.Session, error) {
	return &session{useCase: b.useCase}, nil
}

// session holds one SMTP transaction's accumulated state (RFC 5321 section
// 4.1.1: MAIL, then one or more RCPT, then DATA, then reset).
type session struct {
	useCase            *usecase.ProcessInboundEmailUseCase
	from               string
	recipients         []port.MailboxRecord
	recipientAddresses []string
}

func (s *session) Reset() {
	s.from = ""
	s.recipients = nil
	s.recipientAddresses = nil
}

func (s *session) Logout() error {
	return nil
}

func (s *session) Mail(from string, _ *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	for _, existing := range s.recipientAddresses {
		if existing == to {
			// Duplicate RCPT TO for an address already accepted in this
			// transaction (some MTAs retry): accept silently without
			// persisting a second delivery.
			return nil
		}
	}

	targets, err := s.useCase.ResolveRecipient(context.Background(), to)
	if err != nil {
		switch {
		case errors.Is(err, usecase.ErrRecipientNotFound):
			return &gosmtp.SMTPError{
				Code:         550,
				EnhancedCode: gosmtp.EnhancedCode{5, 1, 1},
				Message:      "User unknown",
			}
		case errors.Is(err, usecase.ErrMailboxQuotaExceeded):
			return &gosmtp.SMTPError{
				Code:         452,
				EnhancedCode: gosmtp.EnhancedCode{4, 2, 2},
				Message:      "Mailbox full",
			}
		default:
			return &gosmtp.SMTPError{
				Code:         451,
				EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
				Message:      "Temporary error resolving recipient",
			}
		}
	}
	// One RCPT TO can resolve to multiple mailboxes (alias fan-out) - each
	// gets its own delivery, all recorded under the same envelope address.
	for _, rec := range targets {
		s.recipients = append(s.recipients, rec)
		s.recipientAddresses = append(s.recipientAddresses, to)
	}
	return nil
}

func (s *session) Data(r io.Reader) error {
	if len(s.recipients) == 0 {
		return &gosmtp.SMTPError{
			Code:         503,
			EnhancedCode: gosmtp.EnhancedCode{5, 5, 1},
			Message:      "RCPT TO required before DATA",
		}
	}
	err := s.useCase.Handle(context.Background(), usecase.ProcessInboundEmailInput{
		Sender:             s.from,
		Recipients:         s.recipients,
		RecipientAddresses: s.recipientAddresses,
		Body:               r,
	})
	if err != nil {
		return &gosmtp.SMTPError{
			Code:         451,
			EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
			Message:      "Temporary error storing message",
		}
	}
	return nil
}
