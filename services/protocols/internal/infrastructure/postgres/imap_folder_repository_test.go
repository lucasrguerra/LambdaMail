package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestImapFolderRepository_FindByName_ReturnsFolderWithUIDData(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "imapfolder-test-" + domainID.String() + ".invalid"
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
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use, uid_next, uid_validity) VALUES ($1, $2, 'INBOX', 'inbox', 5, 12345)`,
		folderID, mailboxID)
	if err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	repo := NewImapFolderRepository(pool)
	rec, err := repo.FindByName(ctx, mailboxID.String(), "INBOX")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec == nil {
		t.Fatal("rec = nil, want a folder record")
	}
	if rec.UIDNext != 5 || rec.UIDValidity != 12345 {
		t.Errorf("rec = %+v, want UIDNext=5 UIDValidity=12345", rec)
	}
}

func TestImapFolderRepository_FindByName_ReturnsNilForUnknownFolder(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewImapFolderRepository(pool)
	rec, err := repo.FindByName(context.Background(), uuid.New().String(), "Nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec != nil {
		t.Errorf("rec = %+v, want nil", rec)
	}
}

func TestImapFolderRepository_ListFolders_ReturnsAllFoldersForMailbox(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "imapfolder-list-" + domainID.String() + ".invalid"
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
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`,
		uuid.New(), mailboxID)
	if err != nil {
		t.Fatalf("seed INBOX: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO folders (id, mailbox_id, name) VALUES ($1, $2, 'Archive')`,
		uuid.New(), mailboxID)
	if err != nil {
		t.Fatalf("seed Archive: %v", err)
	}

	repo := NewImapFolderRepository(pool)
	recs, err := repo.ListFolders(ctx, mailboxID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Errorf("len(recs) = %d, want 2", len(recs))
	}
}
