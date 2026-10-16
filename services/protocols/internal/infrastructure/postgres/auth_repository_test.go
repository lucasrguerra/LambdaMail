package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"lambdamail/protocols/internal/application/port"
)

func TestAuthRepository_FindByAddress_ReturnsHashForActiveMailbox(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "authrepo-test-" + domainID.String() + ".invalid"
	_, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`,
		domainID, domainName, domainName)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	mailboxID := uuid.New()
	address := "user@" + domainName
	hash := "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG"
	_, err = pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', $3, $4)`,
		mailboxID, domainID, address, hash)
	if err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	repo := NewAuthRepository(pool)
	rec, err := repo.FindByAddress(ctx, address)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec == nil {
		t.Fatal("rec = nil, want a record")
	}
	if rec.ID != mailboxID || rec.PasswordHash != hash {
		t.Errorf("rec = %+v, want ID=%v PasswordHash=%q", rec, mailboxID, hash)
	}
}

func TestAuthRepository_FindByAddress_ReturnsNilForUnknownAddress(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	repo := NewAuthRepository(pool)
	rec, err := repo.FindByAddress(context.Background(), "definitely-not-seeded@example.invalid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec != nil {
		t.Errorf("rec = %+v, want nil", rec)
	}
}

// seedAuthMailbox creates a throwaway domain and mailbox and returns the mailbox id
// and address. Cleanup removes the domain, which cascades to the rest.
func seedAuthMailbox(t *testing.T, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()

	domainID := uuid.New()
	domainName := "authrepo-app-" + domainID.String() + ".invalid"
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, $2, $3)`, domainID, domainName, domainName); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID) })

	mailboxID := uuid.New()
	address := "user@" + domainName
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', $3, 'main-hash')`,
		mailboxID, domainID, address); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	return mailboxID, address
}

func TestAuthRepository_FindByAddress_LoadsAppPasswordsWithTheirScopes(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	mailboxID, address := seedAuthMailbox(t, pool)

	if _, err := pool.Exec(ctx, `INSERT INTO app_passwords (mailbox_id, label, password_hash) VALUES ($1, 'phone', 'hash-default-scopes')`, mailboxID); err != nil {
		t.Fatalf("seed app password: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO app_passwords (mailbox_id, label, password_hash, scopes) VALUES ($1, 'reader', 'hash-imap-only', '{imap}')`, mailboxID); err != nil {
		t.Fatalf("seed app password: %v", err)
	}

	rec, err := NewAuthRepository(pool).FindByAddress(ctx, address)
	if err != nil || rec == nil {
		t.Fatalf("FindByAddress: rec=%v err=%v", rec, err)
	}
	if len(rec.AppPasswords) != 2 {
		t.Fatalf("AppPasswords = %d, want 2", len(rec.AppPasswords))
	}

	byHash := map[string][]string{}
	for _, app := range rec.AppPasswords {
		byHash[app.Hash] = app.Scopes
	}
	if got := byHash["hash-default-scopes"]; len(got) != 3 {
		t.Errorf("default scopes = %v, want imap, pop3 and smtp", got)
	}
	if got := byHash["hash-imap-only"]; len(got) != 1 || got[0] != "imap" {
		t.Errorf("imap-only scopes = %v, want [imap]", got)
	}
}

func TestAuthRepository_FindByAddress_AMailboxWithoutAppPasswordsStillWorks(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	_, address := seedAuthMailbox(t, pool)

	rec, err := NewAuthRepository(pool).FindByAddress(context.Background(), address)
	if err != nil || rec == nil {
		t.Fatalf("FindByAddress: rec=%v err=%v", rec, err)
	}
	if len(rec.AppPasswords) != 0 {
		t.Errorf("AppPasswords = %v, want none", rec.AppPasswords)
	}
}

func TestAuthRepository_FindByAddress_ReadsOnlyTheNewestAppPasswords(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	mailboxID, address := seedAuthMailbox(t, pool)

	for i := 0; i < port.MaxAppPasswordsChecked+5; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO app_passwords (mailbox_id, label, password_hash, created_at) VALUES ($1, $2, 'h', NOW() - make_interval(mins => $3))`,
			mailboxID, "label", i); err != nil {
			t.Fatalf("seed app password: %v", err)
		}
	}

	rec, err := NewAuthRepository(pool).FindByAddress(ctx, address)
	if err != nil || rec == nil {
		t.Fatalf("FindByAddress: rec=%v err=%v", rec, err)
	}
	if len(rec.AppPasswords) != port.MaxAppPasswordsChecked {
		t.Errorf("AppPasswords = %d, want the cap of %d", len(rec.AppPasswords), port.MaxAppPasswordsChecked)
	}
}

func TestAuthRepository_RecordAppPasswordUse_StampsLastUsedAt(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()

	mailboxID, _ := seedAuthMailbox(t, pool)

	var appID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO app_passwords (mailbox_id, label, password_hash) VALUES ($1, 'phone', 'h') RETURNING id`, mailboxID).Scan(&appID); err != nil {
		t.Fatalf("seed app password: %v", err)
	}

	if err := NewAuthRepository(pool).RecordAppPasswordUse(ctx, appID); err != nil {
		t.Fatalf("RecordAppPasswordUse: %v", err)
	}

	var stamped bool
	if err := pool.QueryRow(ctx, `SELECT last_used_at IS NOT NULL FROM app_passwords WHERE id = $1`, appID).Scan(&stamped); err != nil || !stamped {
		t.Fatalf("last_used_at not set: stamped=%v err=%v", stamped, err)
	}
}
