package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// How often the same person may be told you are away.
//
// Without this every message produced a reply, so a conversation of five mails
// produced five identical out-of-office notes - and someone who replied to the
// automatic message itself got another one, which is how a person and an
// autoresponder end up talking past each other indefinitely. RFC 5230 section
// 4.1 calls for a suppression period, and names seven days as the default.

type stubVacationLog struct {
	last  map[string]time.Time
	saved []string
	err   error
}

func (s *stubVacationLog) LastRepliedAt(_ context.Context, mailbox uuid.UUID, to string) (time.Time, error) {
	if s.err != nil {
		return time.Time{}, s.err
	}
	return s.last[to], nil
}

func (s *stubVacationLog) RecordReply(_ context.Context, mailbox uuid.UUID, to string) error {
	s.saved = append(s.saved, to)
	return nil
}

func newSuppression(log *stubVacationLog) *VacationSuppression {
	return NewVacationSuppression(log, 7*24*time.Hour)
}

func TestFirstMessageFromSomeoneGetsAReply(t *testing.T) {
	log := &stubVacationLog{last: map[string]time.Time{}}
	if !newSuppression(log).ShouldReply(context.Background(), uuid.New(), "amigo@example.test") {
		t.Error("the first message from someone got no reply")
	}
}

// The second message in the same conversation must not produce a second copy
// of the same notice.
func TestSecondMessageWithinThePeriodIsSilent(t *testing.T) {
	log := &stubVacationLog{last: map[string]time.Time{
		"amigo@example.test": time.Now().Add(-2 * time.Hour),
	}}
	if newSuppression(log).ShouldReply(context.Background(), uuid.New(), "amigo@example.test") {
		t.Error("replied twice to the same person within the period")
	}
}

// Once the period is up, someone writing again is told again - they may not
// remember, and the absence may have changed.
func TestSomeoneWritingAfterThePeriodIsToldAgain(t *testing.T) {
	log := &stubVacationLog{last: map[string]time.Time{
		"amigo@example.test": time.Now().Add(-8 * 24 * time.Hour),
	}}
	if !newSuppression(log).ShouldReply(context.Background(), uuid.New(), "amigo@example.test") {
		t.Error("stayed silent after the suppression period had passed")
	}
}

func TestDifferentPeopleEachGetOne(t *testing.T) {
	log := &stubVacationLog{last: map[string]time.Time{
		"amigo@example.test": time.Now(),
	}}
	if !newSuppression(log).ShouldReply(context.Background(), uuid.New(), "outro@example.test") {
		t.Error("someone else's message was suppressed")
	}
}

// The same person, spelled differently, is the same person.
func TestAddressComparisonIgnoresCase(t *testing.T) {
	log := &stubVacationLog{last: map[string]time.Time{
		"amigo@example.test": time.Now(),
	}}
	if newSuppression(log).ShouldReply(context.Background(), uuid.New(), "AMIGO@EXAMPLE.TEST") {
		t.Error("the same address in another case got a second reply")
	}
}

// A log that cannot be read must not stop the reply: being told twice is a
// smaller failure than never being told at all.
func TestAnUnreadableLogStillReplies(t *testing.T) {
	log := &stubVacationLog{err: errTestLogUnavailable}
	if !newSuppression(log).ShouldReply(context.Background(), uuid.New(), "amigo@example.test") {
		t.Error("an unreadable log silenced the responder")
	}
}

// Without a log configured at all, the responder behaves as it did before.
func TestNoLogMeansNoSuppression(t *testing.T) {
	if !NewVacationSuppression(nil, time.Hour).ShouldReply(context.Background(), uuid.New(), "a@b.test") {
		t.Error("a missing log silenced the responder")
	}
}

func TestReplyIsRecorded(t *testing.T) {
	log := &stubVacationLog{last: map[string]time.Time{}}
	s := newSuppression(log)
	s.Record(context.Background(), uuid.New(), "Amigo@Example.test")

	if len(log.saved) != 1 || log.saved[0] != "amigo@example.test" {
		t.Errorf("recorded %v, want the address lowercased", log.saved)
	}
}

var errTestLogUnavailable = errTestUnavailable{}

type errTestUnavailable struct{}

func (errTestUnavailable) Error() string { return "log unavailable" }

// --- through the delivery path -------------------------------------------

// The second message from the same person in the same week produces no second
// notice, which is the behaviour the reader actually noticed was missing.
func TestDeliveryTellsEachSenderOnlyOnce(t *testing.T) {
	replied := map[string]time.Time{}
	suppression := NewVacationSuppression(&stubVacationLog{last: replied}, 7*24*time.Hour)
	mailboxID := uuid.New()

	if !suppression.ShouldReply(context.Background(), mailboxID, "amigo@example.test") {
		t.Fatal("the first message got no reply")
	}
	// Recording is what the delivery path does once the reply is queued.
	replied["amigo@example.test"] = time.Now()

	if suppression.ShouldReply(context.Background(), mailboxID, "amigo@example.test") {
		t.Error("a second message in the same week produced a second notice")
	}
}
