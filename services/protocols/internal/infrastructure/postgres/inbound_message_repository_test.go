package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/application/port"
)

func TestInboundMessageRepository_Persist_AllocatesSequentialUIDs(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "msgrepo-test-" + domainID.String() + ".invalid"
	_, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	mailboxID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG')`,
		mailboxID, domainID, "user@msgrepo-test-"+domainID.String()+".invalid")
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
	_, err = pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes) VALUES ($1, $2, 'local', '/tmp/x', 10)`,
		blobID, "deadbeef00000000000000000000000000000000000000000000000000000000"[:64])
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	repo := NewInboundMessageRepository(pool)
	blob := port.BlobRef{ID: blobID, SHA256: "deadbeef", SizeBytes: 10}

	firstUID, err := repo.Persist(ctx, port.PersistInboundMessageInput{
		MailboxID: mailboxID, Blob: blob,
		SenderAddress: "sender@example.test", RecipientAddress: "user@msgrepo-test-" + domainID.String() + ".invalid",
		SPFResult: "none", DKIMResult: "none", DMARCResult: "none",
	})
	if err != nil {
		t.Fatalf("first Persist: %v", err)
	}
	secondUID, err := repo.Persist(ctx, port.PersistInboundMessageInput{
		MailboxID: mailboxID, Blob: blob,
		SenderAddress: "sender2@example.test", RecipientAddress: "user@msgrepo-test-" + domainID.String() + ".invalid",
		SPFResult: "none", DKIMResult: "none", DMARCResult: "none",
	})
	if err != nil {
		t.Fatalf("second Persist: %v", err)
	}
	if secondUID <= firstUID {
		t.Errorf("secondUID (%d) must be strictly greater than firstUID (%d) - RFC 3501 section 2.3.1.1", secondUID, firstUID)
	}

	var refCount int
	if err := pool.QueryRow(ctx, `SELECT ref_count FROM message_blobs WHERE id = $1`, blobID).Scan(&refCount); err != nil {
		t.Fatalf("query ref_count: %v", err)
	}
	if refCount != 2 {
		t.Errorf("ref_count = %d, want 2 after two Persist calls referencing the same blob", refCount)
	}
}

func TestInboundMessageRepository_Persist_ReturnsErrorWhenNoInboxFolderExists(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "msgrepo-nofolder-" + domainID.String() + ".invalid"
	_, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	mailboxID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG')`,
		mailboxID, domainID, "nofolder@msgrepo-nofolder-"+domainID.String()+".invalid")
	if err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	// Deliberately no folder row inserted.

	blobID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes) VALUES ($1, $2, 'local', '/tmp/x', 10)`,
		blobID, "cafebabe00000000000000000000000000000000000000000000000000000000"[:64])
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	repo := NewInboundMessageRepository(pool)
	_, err = repo.Persist(ctx, port.PersistInboundMessageInput{
		MailboxID: mailboxID, Blob: port.BlobRef{ID: blobID, SizeBytes: 10},
		SenderAddress: "sender@example.test", RecipientAddress: "nofolder@example.test",
		SPFResult: "none", DKIMResult: "none", DMARCResult: "none",
	})
	if err == nil {
		t.Fatal("expected an error when the mailbox has no INBOX folder, got nil")
	}
}
