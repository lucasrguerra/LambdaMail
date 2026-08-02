package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/application/port"
)

func TestCopyRepository_CopyMessages_AllocatesNewUIDsAndPreservesFlags(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "copyrepo-test-" + domainID.String() + ".invalid"
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
	sourceFolderID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`,
		sourceFolderID, mailboxID)
	if err != nil {
		t.Fatalf("seed source folder: %v", err)
	}
	destFolderID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name) VALUES ($1, $2, 'Archive')`,
		destFolderID, mailboxID)
	if err != nil {
		t.Fatalf("seed dest folder: %v", err)
	}
	blobID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes, ref_count) VALUES ($1, $2, 'local', '/tmp/x', 10, 1)`,
		blobID, testBlobDigest())
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	const sourceSubject = "Original subject line"
	var sourceMessageID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO email_messages (mailbox_id, folder_id, uid, blob_id, sender_address, recipient_addresses, size_bytes, subject)
		VALUES ($1, $2, 5, $3, 'sender@example.test', $4, 10, $5) RETURNING id
	`, mailboxID, sourceFolderID, blobID, []string{"user@" + domainName}, sourceSubject).Scan(&sourceMessageID); err != nil {
		t.Fatalf("seed source message: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO message_flags (message_id, received_at, flag)
		SELECT id, received_at, '\Seen' FROM email_messages WHERE id = $1
	`, sourceMessageID); err != nil {
		t.Fatalf("seed source flag: %v", err)
	}

	repo := NewCopyRepository(pool)
	copied, err := repo.CopyMessages(ctx, sourceFolderID.String(), []uint32{5}, destFolderID.String())
	if err != nil {
		t.Fatalf("CopyMessages: %v", err)
	}
	if len(copied) != 1 || copied[0].SourceUID != 5 || copied[0].DestUID != 1 {
		t.Fatalf("copied = %+v, want [{SourceUID:5 DestUID:1}]", copied)
	}

	var destCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_messages WHERE folder_id = $1`, destFolderID).Scan(&destCount); err != nil {
		t.Fatalf("count dest messages: %v", err)
	}
	if destCount != 1 {
		t.Fatalf("dest folder has %d messages, want 1", destCount)
	}

	var destFlagCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM message_flags mf
		JOIN email_messages m ON m.id = mf.message_id AND m.received_at = mf.received_at
		WHERE m.folder_id = $1 AND mf.flag = '\Seen'
	`, destFolderID).Scan(&destFlagCount); err != nil {
		t.Fatalf("count dest flags: %v", err)
	}
	if destFlagCount != 1 {
		t.Errorf("dest message has %d \\Seen flags, want 1 (flags must be preserved on copy)", destFlagCount)
	}

	var destSubject *string
	if err := pool.QueryRow(ctx, `SELECT subject FROM email_messages WHERE folder_id = $1`, destFolderID).Scan(&destSubject); err != nil {
		t.Fatalf("query dest subject: %v", err)
	}
	if destSubject == nil || *destSubject != sourceSubject {
		t.Errorf("dest subject = %v, want %q (subject must be preserved on copy)", destSubject, sourceSubject)
	}

	var refCount int
	if err := pool.QueryRow(ctx, `SELECT ref_count FROM message_blobs WHERE id = $1`, blobID).Scan(&refCount); err != nil {
		t.Fatalf("query ref_count: %v", err)
	}
	if refCount != 2 {
		t.Errorf("ref_count = %d, want 2 (1 original + 1 copy referencing the same blob)", refCount)
	}

	_ = port.CopiedMessage{} // keep the port import used if the assertions above change
}
