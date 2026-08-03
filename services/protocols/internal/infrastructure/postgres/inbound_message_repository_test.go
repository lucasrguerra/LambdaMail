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
		blobID, testBlobDigest())
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	repo := NewInboundMessageRepository(pool)
	blob := port.BlobRef{ID: blobID, SHA256: "deadbeef", SizeBytes: 10}

	firstUIDs, err := repo.PersistAll(ctx, []port.PersistInboundMessageInput{{
		MailboxID: mailboxID, Blob: blob,
		SenderAddress: "sender@example.test", RecipientAddress: "user@msgrepo-test-" + domainID.String() + ".invalid",
		SPFResult: "none", DKIMResult: "none", DMARCResult: "none",
	}})
	if err != nil {
		t.Fatalf("first PersistAll: %v", err)
	}
	secondUIDs, err := repo.PersistAll(ctx, []port.PersistInboundMessageInput{{
		MailboxID: mailboxID, Blob: blob,
		SenderAddress: "sender2@example.test", RecipientAddress: "user@msgrepo-test-" + domainID.String() + ".invalid",
		SPFResult: "none", DKIMResult: "none", DMARCResult: "none",
	}})
	if err != nil {
		t.Fatalf("second PersistAll: %v", err)
	}
	firstUID, secondUID := firstUIDs[0], secondUIDs[0]
	if secondUID <= firstUID {
		t.Errorf("secondUID (%d) must be strictly greater than firstUID (%d) - RFC 3501 section 2.3.1.1", secondUID, firstUID)
	}

	var refCount int
	if err := pool.QueryRow(ctx, `SELECT ref_count FROM message_blobs WHERE id = $1`, blobID).Scan(&refCount); err != nil {
		t.Fatalf("query ref_count: %v", err)
	}
	if refCount != 2 {
		t.Errorf("ref_count = %d, want 2 after two PersistAll calls referencing the same blob", refCount)
	}

	var unreadCount, totalCount int
	if err := pool.QueryRow(ctx, `SELECT unread_count, total_count FROM folders WHERE id = $1`, folderID).Scan(&unreadCount, &totalCount); err != nil {
		t.Fatalf("query folder counters: %v", err)
	}
	if unreadCount != 2 {
		t.Errorf("folder unread_count = %d, want 2 after two PersistAll calls", unreadCount)
	}
	if totalCount != 2 {
		t.Errorf("folder total_count = %d, want 2 after two PersistAll calls", totalCount)
	}

	var usedBytes int64
	if err := pool.QueryRow(ctx, `SELECT used_bytes FROM mailboxes WHERE id = $1`, mailboxID).Scan(&usedBytes); err != nil {
		t.Fatalf("query mailbox used_bytes: %v", err)
	}
	if usedBytes != blob.SizeBytes*2 {
		t.Errorf("mailbox used_bytes = %d, want %d (two PersistAll calls of %d bytes each)", usedBytes, blob.SizeBytes*2, blob.SizeBytes)
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
		blobID, testBlobDigest())
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	repo := NewInboundMessageRepository(pool)
	_, err = repo.PersistAll(ctx, []port.PersistInboundMessageInput{{
		MailboxID: mailboxID, Blob: port.BlobRef{ID: blobID, SizeBytes: 10},
		SenderAddress: "sender@example.test", RecipientAddress: "nofolder@example.test",
		SPFResult: "none", DKIMResult: "none", DMARCResult: "none",
	}})
	if err == nil {
		t.Fatal("expected an error when the mailbox has no INBOX folder, got nil")
	}
}

// An alias fanning out is one SMTP transaction with one reply. If a later
// recipient's insert fails, the earlier ones must not remain: the sender is
// told to retry, and the retry would deliver to them a second time.
func TestInboundMessageRepository_PersistAll_RollsBackEveryRecipientOnFailure(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "fanout-test-" + domainID.String() + ".invalid"
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	// The first mailbox has an INBOX, the second deliberately does not, so
	// persisting the pair fails on the second entry.
	goodMailboxID, badMailboxID := uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{goodMailboxID, badMailboxID} {
		local := []string{"good", "bad"}[i]
		if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, $3, $4, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG')`,
			id, domainID, local, local+"@"+domainName); err != nil {
			t.Fatalf("seed mailbox %s: %v", local, err)
		}
	}

	folderID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`,
		folderID, goodMailboxID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	blobID := uuid.New()
	blobDigest := testBlobDigest()
	if _, err := pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes) VALUES ($1, $2, 'local', '/tmp/x', 10)`,
		blobID, blobDigest); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM message_blobs WHERE id = $1`, blobID)
	blob := port.BlobRef{ID: blobID, SHA256: blobDigest, SizeBytes: 10}

	repo := NewInboundMessageRepository(pool)
	_, err := repo.PersistAll(ctx, []port.PersistInboundMessageInput{
		{MailboxID: goodMailboxID, Blob: blob, SenderAddress: "sender@remote.test", RecipientAddress: "alias@" + domainName,
			SPFResult: "none", DKIMResult: "none", DMARCResult: "none"},
		{MailboxID: badMailboxID, Blob: blob, SenderAddress: "sender@remote.test", RecipientAddress: "alias@" + domainName,
			SPFResult: "none", DKIMResult: "none", DMARCResult: "none"},
	})
	if err == nil {
		t.Fatal("PersistAll succeeded even though the second recipient has no INBOX")
	}

	var messageCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_messages WHERE mailbox_id = $1`, goodMailboxID).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 0 {
		t.Errorf("the first recipient kept %d message(s) after a failed fan-out; the sender's retry would duplicate them", messageCount)
	}

	var refCount int
	if err := pool.QueryRow(ctx, `SELECT ref_count FROM message_blobs WHERE id = $1`, blobID).Scan(&refCount); err != nil {
		t.Fatalf("query ref_count: %v", err)
	}
	if refCount != 0 {
		t.Errorf("ref_count = %d after a rolled-back delivery, want 0", refCount)
	}
}

// A message the spam filter routes to Junk must still be delivered when the
// mailbox has no Junk folder. Refusing it made the sender retry forever
// against a mailbox that would never grow one on its own.
func TestInboundMessageRepository_FallsBackToInboxWhenFolderMissing(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "fallback-test-" + domainID.String() + ".invalid"
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	mailboxID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash)
		VALUES ($1, $2, 'nojunk', $3, 'x')`, mailboxID, domainID, "nojunk@"+domainName); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	// Only an INBOX exists - deliberately no Junk folder.
	inboxID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`,
		inboxID, mailboxID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	blobID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes) VALUES ($1, $2, 'local', '/tmp/x', 10)`,
		blobID, testBlobDigest()); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM message_blobs WHERE id = $1`, blobID)

	repo := NewInboundMessageRepository(pool)
	uids, err := repo.PersistAll(ctx, []port.PersistInboundMessageInput{{
		MailboxID: mailboxID, Blob: port.BlobRef{ID: blobID, SizeBytes: 10},
		SenderAddress: "spammer@remote.test", RecipientAddress: "nojunk@" + domainName,
		TargetFolderName: "Junk",
		SPFResult:        "none", DKIMResult: "none", DMARCResult: "none",
	}})
	if err != nil {
		t.Fatalf("delivery to a missing folder failed instead of falling back: %v", err)
	}
	if len(uids) != 1 {
		t.Fatalf("got %d uids, want 1", len(uids))
	}

	var folderID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT folder_id FROM email_messages WHERE mailbox_id = $1`, mailboxID).Scan(&folderID); err != nil {
		t.Fatalf("query message: %v", err)
	}
	if folderID != inboxID {
		t.Errorf("message landed in folder %s, want the INBOX %s", folderID, inboxID)
	}
}

// A message this server composed - the Sent copy, or a draft - has no
// authentication results, and the columns holding them are constrained to the
// RFC verdict vocabulary. Passing Go's zero value sent an empty string, which
// satisfies neither the CHECK nor the meaning, and the whole insert failed.
//
// The existing test above always supplied "none", so it never touched this.
// The visible effect was a permanently empty Sent folder - that write is
// non-fatal by design - and "could not save the draft" in the composer.
func TestInboundMessageRepository_PersistsWithoutAuthenticationResults(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "noauth-test-" + domainID.String() + ".invalid"
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	address := "user@" + domainName
	mailboxID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG')`,
		mailboxID, domainID, address); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	for _, f := range []struct{ name, use string }{{"INBOX", "inbox"}, {"Sent", "sent"}, {"Drafts", "drafts"}} {
		if _, err := pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, $3, $4)`,
			uuid.New(), mailboxID, f.name, f.use); err != nil {
			t.Fatalf("seed folder %s: %v", f.name, err)
		}
	}

	blobID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes) VALUES ($1, $2, 'local', '/tmp/x', 10)`,
		blobID, testBlobDigest()); err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	repo := NewInboundMessageRepository(pool)
	blob := port.BlobRef{ID: blobID, SHA256: "deadbeef", SizeBytes: 10}

	for _, folder := range []string{"Sent", "Drafts"} {
		uids, err := repo.PersistAll(ctx, []port.PersistInboundMessageInput{{
			MailboxID: mailboxID, Blob: blob,
			SenderAddress: address, RecipientAddress: address,
			TargetFolderName: folder, Subject: "composed here",
			// Left empty exactly as the compose path leaves them.
		}})
		if err != nil {
			t.Fatalf("PersistAll into %s with no authentication results: %v", folder, err)
		}
		if len(uids) != 1 {
			t.Fatalf("want one uid for %s, got %v", folder, uids)
		}
	}

	// Stored as NULL, not as an empty string: "not evaluated" and "evaluated
	// to nothing" are different answers.
	var nulls int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM email_messages
		 WHERE mailbox_id = $1 AND spf_result IS NULL AND dkim_result IS NULL AND dmarc_result IS NULL
	`, mailboxID).Scan(&nulls); err != nil {
		t.Fatalf("query: %v", err)
	}
	if nulls != 2 {
		t.Errorf("want 2 rows with NULL authentication results, got %d", nulls)
	}
}
