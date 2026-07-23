package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestMessageQueryRepository_ListMessages_ReturnsRowsOrderedByUID(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "msgquery-test-" + domainID.String() + ".invalid"
	_, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	mailboxID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG')`,
		mailboxID, domainID, "user@"+domainName)
	if err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	folderID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`,
		folderID, mailboxID)
	if err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	blobID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes) VALUES ($1, $2, 'local', '/tmp/x', 42)`,
		blobID, "abcdef0000000000000000000000000000000000000000000000000000000000"[:64])
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	for _, uidVal := range []int64{2, 1} { // insert out of order to prove the query sorts
		_, err = pool.Exec(ctx, `
			INSERT INTO email_messages (mailbox_id, folder_id, uid, blob_id, sender_address, recipient_addresses, size_bytes)
			VALUES ($1, $2, $3, $4, 'sender@example.test', $5, 42)
		`, mailboxID, folderID, uidVal, blobID, []string{"user@" + domainName})
		if err != nil {
			t.Fatalf("seed message uid=%d: %v", uidVal, err)
		}
	}

	repo := NewMessageQueryRepository(pool)
	recs, err := repo.ListMessages(ctx, folderID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d, want 2", len(recs))
	}
	if recs[0].UID != 1 || recs[1].UID != 2 {
		t.Errorf("UIDs = [%d, %d], want [1, 2] (ascending order)", recs[0].UID, recs[1].UID)
	}
	if recs[0].BlobID != blobID {
		t.Errorf("BlobID = %v, want %v", recs[0].BlobID, blobID)
	}
}
