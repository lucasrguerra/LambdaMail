package diskstorage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPoolForBlobStorage(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set - skipping blob storage integration test")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Skipf("cannot connect to TEST_DATABASE_URL: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("cannot ping TEST_DATABASE_URL: %v", err)
	}
	return pool
}

func TestLocalDiskBlobStorage_Store_WritesFileAndInsertsBlobRow(t *testing.T) {
	pool := testPoolForBlobStorage(t)
	defer pool.Close()
	dir := t.TempDir()
	storage := NewLocalDiskBlobStorage(pool, dir)

	content := []byte("Subject: test\r\n\r\nhello world")
	ref, err := storage.Store(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM message_blobs WHERE id = $1`, ref.ID)

	if ref.SizeBytes != int64(len(content)) {
		t.Errorf("SizeBytes = %d, want %d", ref.SizeBytes, len(content))
	}

	entries, err := filepath.Glob(filepath.Join(dir, "*", "*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("found %d files under %s, want 1", len(entries), dir)
	}
	written, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !bytes.Equal(written, content) {
		t.Error("written file content does not match input")
	}
}

func TestLocalDiskBlobStorage_Store_DedupsIdenticalContent(t *testing.T) {
	pool := testPoolForBlobStorage(t)
	defer pool.Close()
	dir := t.TempDir()
	storage := NewLocalDiskBlobStorage(pool, dir)

	content := []byte("identical content for dedup test")
	first, err := storage.Store(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first Store: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM message_blobs WHERE id = $1`, first.ID)

	second, err := storage.Store(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second Store: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second.ID = %v, want %v - identical content must dedup to the same blob (PLAN.md section 9)", second.ID, first.ID)
	}
}
