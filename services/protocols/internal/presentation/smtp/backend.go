// Package smtppresentation adapts github.com/emersion/go-smtp's Backend/
// Session interfaces to ProcessInboundEmailUseCase (Clean Architecture
// layer 4 - PLAN.md section 3).
package smtppresentation

import (
	"context"
	"errors"
	"io"
	"log"
	"net"

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

func (b *Backend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	return &session{useCase: b.useCase, conn: c}, nil
}

// session holds one SMTP transaction's accumulated state (RFC 5321 section
// 4.1.1: MAIL, then one or more RCPT, then DATA, then reset).
type session struct {
	useCase            *usecase.ProcessInboundEmailUseCase
	conn               *gosmtp.Conn
	from               string
	recipients         []port.MailboxRecord
	recipientAddresses []string
}

// clientIP is the peer address SPF is evaluated against. It returns nil when
// the connection is not available, which downgrades SPF to "none" rather than
// producing a wrong verdict.
func (s *session) clientIP() net.IP {
	if s.conn == nil || s.conn.Conn() == nil {
		return nil
	}
	if addr, ok := s.conn.Conn().RemoteAddr().(*net.TCPAddr); ok {
		return addr.IP
	}
	host, _, err := net.SplitHostPort(s.conn.Conn().RemoteAddr().String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

func (s *session) heloDomain() string {
	if s.conn == nil {
		return ""
	}
	return s.conn.Hostname()
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
		ClientIP:           s.clientIP(),
		HeloDomain:         s.heloDomain(),
	})
	if err != nil {
		var smtpErr *gosmtp.SMTPError
		if errors.As(err, &smtpErr) {
			return smtpErr
		}

		// A refusal the use case decided on carries its own reply. Rendering
		// it here is the only place the wire format is chosen.
		var rejection *usecase.SmtpRejection
		if errors.As(err, &rejection) {
			return &gosmtp.SMTPError{
				Code: rejection.Code,
				EnhancedCode: gosmtp.EnhancedCode{
					rejection.EnhancedCode[0], rejection.EnhancedCode[1], rejection.EnhancedCode[2],
				},
				Message: rejection.Message,
			}
		}

		// Anything else is an internal failure. It is reported as temporary so
		// the sender retries: the message was not stored, and telling the peer
		// it failed permanently would discard mail over a bug on this side.
		//
		// Logged because the peer only ever sees "temporary error"; without
		// this line an operator has a bounced delivery and no cause.
		log.Printf("smtp: could not store message from %s: %v", s.from, err)
		return &gosmtp.SMTPError{
			Code:         451,
			EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
			Message:      "Temporary error storing message",
		}
	}
	return nil
}
