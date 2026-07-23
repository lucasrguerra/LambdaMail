package port

import (
	"context"
	"io"

	"github.com/google/uuid"
)

// BlobRef identifies a stored message blob (PLAN.md section 9, message_blobs).
type BlobRef struct {
	ID        uuid.UUID
	SHA256    string
	SizeBytes int64
}

// BlobStorage persists raw message bytes, deduplicated by content hash.
type BlobStorage interface {
	// Store writes r to durable storage and returns its BlobRef. Calling
	// Store twice with identical content returns the same BlobRef.ID.
	Store(ctx context.Context, r io.Reader) (BlobRef, error)
}
