package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestAliasRepository_EnsureSystemAliases(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'dnsalias.test', 'dnsalias.test')`, domainID); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM domains WHERE id = $1`, domainID)

	repo := NewAliasRepository(pool)

	if err := repo.EnsureSystemAliases(ctx, "dnsalias.test", "admin@dnsalias.test"); err != nil {
		t.Fatalf("EnsureSystemAliases: %v", err)
	}

	// Verify postmaster, abuse, dmarc, tlsrpt aliases exist
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM aliases WHERE domain_id = $1 AND is_system = true`, domainID).Scan(&count); err != nil || count != 4 {
		t.Fatalf("expected 4 system aliases in DB, got count=%d, err=%v", count, err)
	}
}
