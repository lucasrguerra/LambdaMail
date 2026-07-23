package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MessageRecord is one message's IMAP-relevant data.
type MessageRecord struct {
	UID        uint32
	BlobID     uuid.UUID
	SizeBytes  int64
	ReceivedAt time.Time
	Flags      []string
}

// MessageQueryRepository lists a folder's messages for FETCH.
type MessageQueryRepository interface {
	// ListMessages returns every message in folderID, ordered by UID
	// ascending. The caller derives IMAP sequence numbers from this order
	// (row N is sequence number N+1) - safe because this sub-project never
	// expunges a message, so sequence numbers never need remapping.
	ListMessages(ctx context.Context, folderID string) ([]MessageRecord, error)
}
