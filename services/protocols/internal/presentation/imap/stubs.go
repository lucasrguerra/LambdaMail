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
	"github.com/emersion/go-imap/v2/imapserver"
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
// Poll is called by imapserver before the status response of nearly every
// command in the Authenticated/Selected state (see conn.go's c.poll), not
// just in response to an explicit client IDLE/NOOP - returning an error here
// would fail every real command (FETCH, STORE, ...), not just polling. This
// sub-project doesn't push unsolicited EXISTS/EXPUNGE/FETCH updates (no
// IDLE support - see Idle below), so reporting "no updates" via a nil
// return is the correct, spec-compliant no-op, not a stub.
func (s *session) Poll(_ *imapserver.UpdateWriter, _ bool) error { return nil }
func (s *session) Idle(_ *imapserver.UpdateWriter, _ <-chan struct{}) error {
	return errNotYetImplemented
}
func (s *session) Expunge(_ *imapserver.ExpungeWriter, _ *imap.UIDSet) error {
	return errNotYetImplemented
}
func (s *session) Search(_ imapserver.NumKind, _ *imap.SearchCriteria, _ *imap.SearchOptions) (*imap.SearchData, error) {
	return nil, errNotYetImplemented
}
func (s *session) Copy(_ imap.NumSet, _ string) (*imap.CopyData, error) {
	return nil, errNotYetImplemented
}
