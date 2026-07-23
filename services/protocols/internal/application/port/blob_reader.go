package port

import (
	"context"

	"github.com/google/uuid"
)

// BlobReader reads a previously stored message's full raw bytes.
type BlobReader interface {
	ReadByID(ctx context.Context, blobID uuid.UUID) ([]byte, error)
}
