package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runMigration0014 applies the migration file itself, so the test cannot drift
// from what production runs. It is idempotent by design, which is also tested.
func runMigration0014(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := os.ReadFile("../../../../../migrations/0014_mailbox_state_single_source.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("apply migration 0014: %v", err)
	}
}

func folderNames(t *testing.T, pool *pgxpool.Pool, mailboxID string) map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT name, COALESCE(special_use, '') FROM folders WHERE mailbox_id = $1`, mailboxID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, role string
		if err := rows.Scan(&name, &role); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[name] = role
	}
	return out
}

// Numbers that two paths left wrong are corrected from the rows.
func TestMigration0014_ReconcilesDriftedCountersAndQuota(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	mailboxID := seedMailbox(t, pool)
	deliver(t, pool, mailboxID, "INBOX", "a", false)
	deliver(t, pool, mailboxID, "INBOX", "b", true)
	deliver(t, pool, mailboxID, "Sent", "c", true)

	// The drift as it was found: counters and quota that disagree with the rows
	if _, err := pool.Exec(ctx, `UPDATE folders SET unread_count = 9, total_count = 7 WHERE mailbox_id = $1`, mailboxID); err != nil {
		t.Fatalf("corrupt counters: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mailboxes SET used_bytes = 12345 WHERE id = $1`, mailboxID); err != nil {
		t.Fatalf("corrupt quota: %v", err)
	}

	runMigration0014(t, pool)
	assertConsistent(t, pool, mailboxID, "the reconciliation")

	runMigration0014(t, pool)
	assertConsistent(t, pool, mailboxID, "running the reconciliation a second time")
}

// A mailbox that is already right is left exactly as it was.
func TestMigration0014_LeavesATrueMailboxAlone(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	mailboxID := seedMailbox(t, pool)
	deliver(t, pool, mailboxID, "INBOX", "a", false)
	before, usedBefore, _ := mailState(t, pool, mailboxID)

	runMigration0014(t, pool)

	after, usedAfter, _ := mailState(t, pool, mailboxID)
	for name, f := range before {
		if after[name].CachedTotal != f.CachedTotal || after[name].CachedUnread != f.CachedUnread || after[name].HighestModSeq != f.HighestModSeq {
			t.Errorf("%s changed: %+v -> %+v", name, f, after[name])
		}
	}
	if usedAfter != usedBefore {
		t.Errorf("used_bytes changed from %d to %d", usedBefore, usedAfter)
	}
	_ = ctx
}

// The one list of standard folders: what is missing is added, what is there is
// not duplicated, and running it again changes nothing.
func TestProvisionDefaultFolders(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	runMigration0014(t, pool)

	domainID, mailboxID := uuid.New(), uuid.New()
	name := "prov" + uuid.NewString()[:8] + ".test"
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name, is_active) VALUES ($1,$2,$3,true)`, domainID, name, name); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM domains WHERE id = $1`, domainID) })
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, is_active) VALUES ($1,$2,'u',$3,'x',true)`,
		mailboxID, domainID, "u@"+name); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	want := map[string]string{"INBOX": "inbox", "Sent": "sent", "Drafts": "drafts", "Trash": "trash", "Junk": "junk", "Archive": "archive", "Reports": ""}

	if _, err := pool.Exec(ctx, `SELECT provision_default_folders($1)`, mailboxID); err != nil {
		t.Fatalf("provision: %v", err)
	}
	got := folderNames(t, pool, mailboxID.String())
	for n, role := range want {
		if r, ok := got[n]; !ok || r != role {
			t.Errorf("folder %q: present=%v role=%q, want role %q", n, ok, r, role)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d folders, want %d: %v", len(got), len(want), got)
	}

	if _, err := pool.Exec(ctx, `SELECT provision_default_folders($1)`, mailboxID); err != nil {
		t.Fatalf("provision again: %v", err)
	}
	if again := folderNames(t, pool, mailboxID.String()); len(again) != len(want) {
		t.Errorf("a second call changed the folder count to %d", len(again))
	}
}

// A folder that exists under another capitalisation counts as present, and the
// folder keeps the owner's mail.
func TestProvisionDefaultFolders_DoesNotDuplicateByCase(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	runMigration0014(t, pool)
	mailboxID := seedMailbox(t, pool) // INBOX, Sent, Drafts, Trash

	if _, err := pool.Exec(ctx, `INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1, 'reports', NULL)`, mailboxID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT provision_default_folders($1)`, mailboxID); err != nil {
		t.Fatalf("provision: %v", err)
	}

	got := folderNames(t, pool, mailboxID)
	reports := 0
	for n := range got {
		if n == "reports" || n == "Reports" {
			reports++
		}
	}
	if reports != 1 {
		t.Errorf("found %d reports folders, want 1: %v", reports, got)
	}
	if len(got) != 7 {
		t.Errorf("got %d folders, want 7: %v", len(got), got)
	}
}
