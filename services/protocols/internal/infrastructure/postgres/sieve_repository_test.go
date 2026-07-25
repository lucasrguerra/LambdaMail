package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestSieveRepository_CRUD(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set - skipping Postgres integration test")
	}

	ctx := context.Background()
	pool, err := NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	domainID := uuid.New()
	mailboxID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'sieve.test', 'sieve.test')`, domainID); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'sieve', 'sieve@sieve.test', 'hash')`, mailboxID, domainID); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	repo := NewSieveRepository(pool)

	// 1. PutScript
	if err := repo.PutScript(ctx, mailboxID.String(), "script1", "keep;"); err != nil {
		t.Fatalf("PutScript: %v", err)
	}

	// 2. GetScript
	rec, err := repo.GetScript(ctx, mailboxID.String(), "script1")
	if err != nil || rec == nil || rec.Script != "keep;" {
		t.Fatalf("GetScript: rec=%+v, err=%v", rec, err)
	}

	// 3. SetActive
	if err := repo.SetActiveScript(ctx, mailboxID.String(), "script1"); err != nil {
		t.Fatalf("SetActiveScript: %v", err)
	}
	rec, _ = repo.GetScript(ctx, mailboxID.String(), "script1")
	if !rec.IsActive {
		t.Fatal("expected is_active = true")
	}

	// 4. RenameScript
	if err := repo.RenameScript(ctx, mailboxID.String(), "script1", "script1_renamed"); err != nil {
		t.Fatalf("RenameScript: %v", err)
	}

	// 5. Deactivate and Delete
	if err := repo.SetActiveScript(ctx, mailboxID.String(), ""); err != nil {
		t.Fatalf("SetActiveScript empty: %v", err)
	}
	if err := repo.DeleteScript(ctx, mailboxID.String(), "script1_renamed"); err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}

	// 6. Confirm deleted
	rec, _ = repo.GetScript(ctx, mailboxID.String(), "script1_renamed")
	if rec != nil {
		t.Fatal("expected script to be deleted, got non-nil record")
	}
}
