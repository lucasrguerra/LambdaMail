package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
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
