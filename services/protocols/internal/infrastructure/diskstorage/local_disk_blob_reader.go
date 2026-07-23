package diskstorage

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LocalDiskBlobReader implements port.BlobReader by looking up the blob's
// storage_path in Postgres, then reading the full file from local disk.
type LocalDiskBlobReader struct {
	pool *pgxpool.Pool
}

func NewLocalDiskBlobReader(pool *pgxpool.Pool) *LocalDiskBlobReader {
	return &LocalDiskBlobReader{pool: pool}
}

func (r *LocalDiskBlobReader) ReadByID(ctx context.Context, blobID uuid.UUID) ([]byte, error) {
	var path string
	if err := r.pool.QueryRow(ctx, `SELECT storage_path FROM message_blobs WHERE id = $1`, blobID).Scan(&path); err != nil {
		return nil, fmt.Errorf("find blob %s: %w", blobID, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read blob file %s: %w", path, err)
	}
	return data, nil
}
