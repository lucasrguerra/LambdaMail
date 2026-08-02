// Package imappresentation adapts github.com/emersion/go-imap/v2's
// imapserver.Session interface to ImapSessionUseCase (Clean Architecture
// layer 4 - PLAN.md section 3). This file holds every method NOT
// implemented for real in this sub-project: each returns a clear IMAP NO
// error, never a panic, never a silent no-op (see the design doc's
// "all-or-nothing interface problem" section for why every one of these
// 18 methods must exist even though only 7 do real work here).
package imappresentation

import (
	"github.com/emersion/go-imap/v2"
)

var errNotYetImplemented = &imap.Error{
	Type: imap.StatusResponseTypeNo,
	Text: "not yet implemented",
}

func (s *session) Create(_ string, _ *imap.CreateOptions) error    { return errNotYetImplemented }
func (s *session) Delete(_ string) error                           { return errNotYetImplemented }
func (s *session) Rename(_, _ string, _ *imap.RenameOptions) error { return errNotYetImplemented }
func (s *session) Subscribe(_ string) error                        { return errNotYetImplemented }
func (s *session) Unsubscribe(_ string) error                      { return errNotYetImplemented }
func (s *session) Append(_ string, _ imap.LiteralReader, _ *imap.AppendOptions) (*imap.AppendData, error) {
	return nil, errNotYetImplemented
}
