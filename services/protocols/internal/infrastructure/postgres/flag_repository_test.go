package postgres

import (
	"context"
	"sort"
	"testing"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/application/port"
)

func TestFlagRepository_SetFlags_AddThenDelThenSet(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "flagrepo-test-" + domainID.String() + ".invalid"
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
	_, err = pool.Exec(ctx, `INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes) VALUES ($1, $2, 'local', '/tmp/x', 10)`,
		blobID, "1111110000000000000000000000000000000000000000000000000000000000"[:64])
	if err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	messageID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO email_messages (id, mailbox_id, folder_id, uid, blob_id, sender_address, recipient_addresses, size_bytes)
		VALUES ($1, $2, $3, 7, $4, 'sender@example.test', $5, 10)
	`, messageID, mailboxID, folderID, blobID, []string{"user@" + domainName})
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}

	readFlags := func() []string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT flag FROM message_flags WHERE message_id = $1 ORDER BY flag`, messageID)
		if err != nil {
			t.Fatalf("query flags: %v", err)
		}
		defer rows.Close()
		var flags []string
		for rows.Next() {
			var f string
			if err := rows.Scan(&f); err != nil {
				t.Fatalf("scan flag: %v", err)
			}
			flags = append(flags, f)
		}
		return flags
	}

	repo := NewFlagRepository(pool)

	if updated, err := repo.SetFlags(ctx, folderID.String(), 7, port.FlagOpAdd, []string{"\\Seen", "\\Flagged"}, 0); err != nil || !updated {
		t.Fatalf("add: updated=%v, err=%v", updated, err)
	}
	got := readFlags()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "\\Flagged" || got[1] != "\\Seen" {
		t.Fatalf("after add: flags = %v, want [\\Flagged \\Seen]", got)
	}

	if updated, err := repo.SetFlags(ctx, folderID.String(), 7, port.FlagOpDel, []string{"\\Flagged"}, 0); err != nil || !updated {
		t.Fatalf("del: updated=%v, err=%v", updated, err)
	}
	got = readFlags()
	if len(got) != 1 || got[0] != "\\Seen" {
		t.Fatalf("after del: flags = %v, want [\\Seen]", got)
	}

	if updated, err := repo.SetFlags(ctx, folderID.String(), 7, port.FlagOpSet, []string{"\\Answered"}, 0); err != nil || !updated {
		t.Fatalf("set: updated=%v, err=%v", updated, err)
	}
	got = readFlags()
	if len(got) != 1 || got[0] != "\\Answered" {
		t.Fatalf("after set: flags = %v, want [\\Answered] (Set replaces the full flag set)", got)
	}

	// A client sending "STORE 1 FLAGS (\Seen \Seen)" - a duplicate flag in a
	// single command - must not fail the whole STORE with a primary key
	// violation on the second INSERT for the same (message_id, received_at,
	// flag).
	if updated, err := repo.SetFlags(ctx, folderID.String(), 7, port.FlagOpSet, []string{"\\Seen", "\\Seen"}, 0); err != nil || !updated {
		t.Fatalf("set with duplicate flags in input: updated=%v, err=%v", updated, err)
	}
	got = readFlags()
	if len(got) != 1 || got[0] != "\\Seen" {
		t.Fatalf("after set with duplicate input flags: flags = %v, want [\\Seen]", got)
	}

	// Test UNCHANGEDSINCE conditional store:
	// Message's modseq was updated during the previous SetFlags call.
	// Providing a lower unchangedSince value must fail (return updated=false).
	if updated, err := repo.SetFlags(ctx, folderID.String(), 7, port.FlagOpSet, []string{"\\Flagged"}, 1); err != nil || updated {
		t.Fatalf("conditional set with lower modseq: expected updated=false, got updated=%v, err=%v", updated, err)
	}
	// Providing a matching or higher unchangedSince value must succeed (return updated=true).
	if updated, err := repo.SetFlags(ctx, folderID.String(), 7, port.FlagOpSet, []string{"\\Flagged"}, 100); err != nil || !updated {
		t.Fatalf("conditional set with higher modseq: expected updated=true, got updated=%v, err=%v", updated, err)
	}
}
