package port

import "context"

// ExpungeRepository permanently marks messages as expunged (soft delete -
// email_messages.expunged_at is set, never a hard DELETE). Blob ref_count
// is deliberately untouched here; physical blob garbage collection is a
// separate future concern.
type ExpungeRepository interface {
	// Expunge marks each message identified by (folderID, uid) in uids as
	// expunged, decrementing the folder's total_count (and unread_count for
	// any message that lacked \Seen). The caller is responsible for having
	// already determined that every UID in uids actually carries \Deleted -
	// this method does not re-check that itself.
	Expunge(ctx context.Context, folderID string, uids []uint32) error
}
