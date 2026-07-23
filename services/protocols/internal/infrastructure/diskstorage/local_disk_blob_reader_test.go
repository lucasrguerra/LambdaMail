package diskstorage

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestLocalDiskBlobReader_ReadByID_ReturnsStoredContent(t *testing.T) {
	pool := testPoolForBlobStorage(t)
	defer pool.Close()
	dir := t.TempDir()
	storage := NewLocalDiskBlobStorage(pool, dir)
	reader := NewLocalDiskBlobReader(pool)

	content := []byte("Subject: read test\r\n\r\nhello reader")
	ref, err := storage.Store(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	defer pool.Exec(context.Background(), `DELETE FROM message_blobs WHERE id = $1`, ref.ID)

	got, err := reader.ReadByID(context.Background(), ref.ID)
	if err != nil {
		t.Fatalf("ReadByID: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("ReadByID content = %q, want %q", got, content)
	}
}

func TestLocalDiskBlobReader_ReadByID_ReturnsErrorForUnknownID(t *testing.T) {
	pool := testPoolForBlobStorage(t)
	defer pool.Close()
	reader := NewLocalDiskBlobReader(pool)

	_, err := reader.ReadByID(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected an error for an unknown (random) blob ID, got nil")
	}
}
