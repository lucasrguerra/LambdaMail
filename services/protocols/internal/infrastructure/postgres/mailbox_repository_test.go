package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool returns a pool for TEST_DATABASE_URL, or skips the test if that
// env var is unset or the database is unreachable - these are integration
// tests against a real Postgres, not unit tests.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set - skipping Postgres integration test")
	}
	pool, err := NewPool(context.Background(), dbURL)
	if err != nil {
		t.Skipf("cannot connect to TEST_DATABASE_URL: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("cannot ping TEST_DATABASE_URL: %v", err)
	}
	return pool
}

func TestMailboxRepository_FindActiveByAddress_ReturnsNilForUnknownAddress(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	repo := NewMailboxRepository(pool)

	rec, err := repo.FindActiveByAddress(context.Background(), "definitely-not-seeded@example.invalid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec != nil {
		t.Errorf("rec = %+v, want nil for an address with no matching mailbox", rec)
	}
}

func TestMailboxRepository_FindActiveByAddress_ReturnsRecordForSeededMailbox(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "repo-test-" + domainID.String() + ".invalid"
	// NOTE: reusing a single placeholder ($2) for both `name` (CITEXT) and
	// `punycode_name` (VARCHAR) makes Postgres's extended-protocol parameter
	// type inference fail with "inconsistent types deduced for parameter $2"
	// (SQLSTATE 42P08), confirmed via isolated repro against this same DB -
	// see task-3-report.md. Using distinct placeholders for the same value
	// avoids that false-negative without changing what's inserted.
	_, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name, max_message_bytes) VALUES ($1, $2, $3, 12345)`,
		domainID, domainName, domainName)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	mailboxID := uuid.New()
	address := "user@repo-test-" + domainID.String() + ".invalid"
	_, err = pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG')`,
		mailboxID, domainID, address)
	if err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	repo := NewMailboxRepository(pool)
	rec, err := repo.FindActiveByAddress(ctx, address)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec == nil {
		t.Fatal("rec = nil, want a record for the seeded active mailbox")
	}
	if rec.ID != mailboxID {
		t.Errorf("ID = %v, want %v", rec.ID, mailboxID)
	}
	if rec.MaxMessageBytes != 12345 {
		t.Errorf("MaxMessageBytes = %d, want 12345 (from the domain row)", rec.MaxMessageBytes)
	}
}

func TestMailboxRepository_FindActiveByAddress_ExcludesInactiveMailbox(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "repo-test-inactive-" + domainID.String() + ".invalid"
	// See NOTE above TestMailboxRepository_FindActiveByAddress_ReturnsRecordForSeededMailbox
	// re: distinct placeholders for `name`/`punycode_name`.
	_, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	address := "user@repo-test-inactive-" + domainID.String() + ".invalid"
	_, err = pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, is_active) VALUES ($1, $2, 'user', $3, '$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG', false)`,
		uuid.New(), domainID, address)
	if err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	repo := NewMailboxRepository(pool)
	rec, err := repo.FindActiveByAddress(ctx, address)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec != nil {
		t.Errorf("rec = %+v, want nil - mailbox is_active=false must be excluded", rec)
	}
}
