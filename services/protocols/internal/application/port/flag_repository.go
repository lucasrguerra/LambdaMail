package port

import "context"

// FlagOp mirrors imap.StoreFlagsOp without importing the imap package here
// (this port stays framework-free).
type FlagOp int

const (
	FlagOpAdd FlagOp = iota
	FlagOpDel
	FlagOpSet
)

// FlagRepository mutates a message's IMAP flags (STORE).
type FlagRepository interface {
	// SetFlags applies op with flags to the message identified by
	// (folderID, uid). Add/Del insert/delete individual message_flags rows;
	// Set replaces the message's full flag set in one transaction. If
	// unchangedSince > 0 and the message's current modseq exceeds it, the update
	// is skipped and updated returns false.
	SetFlags(ctx context.Context, folderID string, uid uint32, op FlagOp, flags []string, unchangedSince uint64) (updated bool, err error)
}
