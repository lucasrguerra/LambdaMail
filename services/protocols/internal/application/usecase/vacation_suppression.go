package usecase

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// VacationSuppression decides whether someone has already been told.
//
// Without it every message produced a reply: a conversation of five mails
// produced five identical out-of-office notes, and someone who replied to the
// automatic message itself was answered again - which is how a person and an
// autoresponder end up talking past each other indefinitely.
//
// RFC 5230 section 4.1 calls for exactly this, and names seven days as the
// default period.
type VacationSuppression struct {
	log    port.VacationLog
	period time.Duration
}

// DefaultVacationPeriod is how long one notice stands for, per RFC 5230.
const DefaultVacationPeriod = 7 * 24 * time.Hour

func NewVacationSuppression(vacationLog port.VacationLog, period time.Duration) *VacationSuppression {
	if period <= 0 {
		period = DefaultVacationPeriod
	}
	return &VacationSuppression{log: vacationLog, period: period}
}

// ShouldReply reports whether this sender may be told again.
//
// Fails open: a log that cannot be read still lets the reply out. Being told
// twice is a smaller failure than someone never learning the mailbox is
// unattended, which is the whole purpose of the feature.
func (s *VacationSuppression) ShouldReply(ctx context.Context, mailboxID uuid.UUID, to string) bool {
	if s == nil || s.log == nil {
		return true
	}
	address := normaliseAddress(to)
	if address == "" {
		return false
	}

	last, err := s.log.LastRepliedAt(ctx, mailboxID, address)
	if err != nil {
		log.Printf("vacation: could not read the reply log for %s, replying anyway: %v", address, err)
		return true
	}
	if last.IsZero() {
		return true
	}
	return time.Since(last) >= s.period
}

// Record notes that this sender has been told.
func (s *VacationSuppression) Record(ctx context.Context, mailboxID uuid.UUID, to string) {
	if s == nil || s.log == nil {
		return
	}
	address := normaliseAddress(to)
	if address == "" {
		return
	}
	if err := s.log.RecordReply(ctx, mailboxID, address); err != nil {
		// Logged and swallowed: the reply has already gone out, and failing
		// here would only mean the caller thinks it did not.
		log.Printf("vacation: could not record the reply to %s: %v", address, err)
	}
}

// normaliseAddress makes one person one key, however their address was spelled.
func normaliseAddress(raw string) string {
	address := strings.TrimSpace(strings.ToLower(raw))
	if angled := strings.LastIndex(address, "<"); angled >= 0 {
		if close := strings.Index(address[angled:], ">"); close > 0 {
			address = address[angled+1 : angled+close]
		}
	}
	return strings.TrimSpace(address)
}
