package port

import "context"

// CopiedMessage records one message's UID before and after a COPY/MOVE.
type CopiedMessage struct {
	SourceUID uint32
	DestUID   uint32
}

// CopyRepository duplicates messages into another folder, preserving their
// blob reference (content-hash dedup - PLAN.md section 9) and flags.
type CopyRepository interface {
	// CopyMessages copies each message identified by (sourceFolderID, uid)
	// in uids into destFolderID, allocating a new UID in the destination
	// per message, preserving flags, and incrementing the destination
	// folder's total_count/unread_count and the destination mailbox's
	// used_bytes accordingly. Returns one CopiedMessage per input uid, in
	// the same order as uids.
	CopyMessages(ctx context.Context, sourceFolderID string, uids []uint32, destFolderID string) ([]CopiedMessage, error)
}
