package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestExpungeRepository_Expunge_SetsExpungedAtAndDecrementsCounters(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "expunge-test-" + domainID.String() + ".invalid"
	_, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	mailboxID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, used_bytes) VALUES ($1, $2, 'user', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG', 20)`,
		mailboxID, domainID, "user@"+domainName)
	if err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	folderID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use, total_count, unread_count) VALUES ($1, $2, 'INBOX', 'inbox', 2, 2)`,
		folderID, mailboxID)
	if err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	blobID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes) VALUES ($1, $2, 'local', '/tmp/x', 10)`,
		blobID, testBlobDigest())
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	var msg1ID, msg2ID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO email_messages (mailbox_id, folder_id, uid, blob_id, sender_address, recipient_addresses, size_bytes)
		VALUES ($1, $2, 1, $3, 'sender@example.test', $4, 10) RETURNING id
	`, mailboxID, folderID, blobID, []string{"user@" + domainName}).Scan(&msg1ID); err != nil {
		t.Fatalf("seed message 1: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO email_messages (mailbox_id, folder_id, uid, blob_id, sender_address, recipient_addresses, size_bytes)
		VALUES ($1, $2, 2, $3, 'sender@example.test', $4, 10) RETURNING id
	`, mailboxID, folderID, blobID, []string{"user@" + domainName}).Scan(&msg2ID); err != nil {
		t.Fatalf("seed message 2: %v", err)
	}

	repo := NewExpungeRepository(pool)
	if err := repo.Expunge(ctx, folderID.String(), []uint32{1}); err != nil {
		t.Fatalf("Expunge: %v", err)
	}

	var expunged1 any
	if err := pool.QueryRow(ctx, `SELECT expunged_at FROM email_messages WHERE id = $1`, msg1ID).Scan(&expunged1); err != nil {
		t.Fatalf("query message 1: %v", err)
	}
	if expunged1 == nil {
		t.Error("message 1 (uid=1, expunged) has expunged_at = NULL, want a timestamp")
	}

	var expunged2 any
	if err := pool.QueryRow(ctx, `SELECT expunged_at FROM email_messages WHERE id = $1`, msg2ID).Scan(&expunged2); err != nil {
		t.Fatalf("query message 2: %v", err)
	}
	if expunged2 != nil {
		t.Error("message 2 (uid=2, not expunged) has a non-NULL expunged_at, want NULL")
	}

	var totalCount, unreadCount int
	if err := pool.QueryRow(ctx, `SELECT total_count, unread_count FROM folders WHERE id = $1`, folderID).Scan(&totalCount, &unreadCount); err != nil {
		t.Fatalf("query folder counters: %v", err)
	}
	if totalCount != 1 {
		t.Errorf("total_count = %d, want 1 (started at 2, expunged 1)", totalCount)
	}
	if unreadCount != 1 {
		t.Errorf("unread_count = %d, want 1 (expunged message had no \\Seen flag, so it counted as unread)", unreadCount)
	}

	var usedBytes int64
	if err := pool.QueryRow(ctx, `SELECT used_bytes FROM mailboxes WHERE id = $1`, mailboxID).Scan(&usedBytes); err != nil {
		t.Fatalf("query mailbox used_bytes: %v", err)
	}
	if usedBytes != 10 {
		t.Errorf("used_bytes = %d, want 10 (started at 20, expunged message 1 with size_bytes=10)", usedBytes)
	}
}
