// Package diskstorage implements port.BlobStorage on the local filesystem.
package diskstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// LocalDiskBlobStorage writes message bodies under baseDir, sharded into
// subdirectories by the first two hex chars of the content hash (avoids
// unbounded single-directory fanout).
type LocalDiskBlobStorage struct {
	pool    *pgxpool.Pool
	baseDir string
}

func NewLocalDiskBlobStorage(pool *pgxpool.Pool, baseDir string) *LocalDiskBlobStorage {
	return &LocalDiskBlobStorage{pool: pool, baseDir: baseDir}
}

func (s *LocalDiskBlobStorage) Store(ctx context.Context, r io.Reader) (port.BlobRef, error) {
	tmp, err := os.CreateTemp(s.baseDir, "incoming-*.tmp")
	if err != nil {
		return port.BlobRef{}, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed to its final path

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hasher), r)
	if err != nil {
		tmp.Close()
		return port.BlobRef{}, fmt.Errorf("write blob: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return port.BlobRef{}, fmt.Errorf("fsync blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return port.BlobRef{}, fmt.Errorf("close blob: %w", err)
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	shardDir := filepath.Join(s.baseDir, sum[:2])
	if err := os.MkdirAll(shardDir, 0o755); err != nil {
		return port.BlobRef{}, fmt.Errorf("create shard dir: %w", err)
	}
	finalPath := filepath.Join(shardDir, sum+".eml")

	// Rename into place BEFORE inserting the DB row, so a committed row
	// never references a file that isn't actually there yet (a rename
	// failure after an insert would otherwise leave a durable row pointing
	// at nothing, and any concurrent Store with identical content could
	// dedup onto that row and report success for content that was never
	// safely stored).
	if _, statErr := os.Stat(finalPath); statErr == nil {
		// Content-identical file already stored (same hash => same path,
		// and file content is byte-identical); no need to overwrite it.
		os.Remove(tmpPath)
	} else if !os.IsNotExist(statErr) {
		return port.BlobRef{}, fmt.Errorf("stat final blob path: %w", statErr)
	} else {
		if err := os.Rename(tmpPath, finalPath); err != nil {
			return port.BlobRef{}, fmt.Errorf("rename blob into place: %w", err)
		}
	}

	// Insert-or-fetch: identical content dedups to the existing row.
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO message_blobs (content_sha256, storage_driver, storage_path, size_bytes)
		VALUES ($1, 'local', $2, $3)
		ON CONFLICT (content_sha256) DO NOTHING
		RETURNING id
	`, sum, finalPath, size).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Conflict: a row already exists for this hash.
			err = s.pool.QueryRow(ctx, `SELECT id FROM message_blobs WHERE content_sha256 = $1`, sum).Scan(&id)
			if err != nil {
				return port.BlobRef{}, fmt.Errorf("fetch existing blob row: %w", err)
			}
			// Content already stored on disk from a prior call; discard this duplicate write.
			return port.BlobRef{ID: id, SHA256: sum, SizeBytes: size}, nil
		}
		return port.BlobRef{}, fmt.Errorf("insert blob row: %w", err)
	}

	return port.BlobRef{ID: id, SHA256: sum, SizeBytes: size}, nil
}
