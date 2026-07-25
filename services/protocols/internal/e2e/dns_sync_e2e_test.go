package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/infrastructure/postgres"
)

func TestDnsSyncAndSystemAliasesEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54336).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54336/postgres?sslmode=disable"
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()

	root := repoRoot(t)
	sql, err := os.ReadFile(filepath.Join(root, "migrations", "0001_init_schema.up.sql"))
	if err != nil {
		t.Fatalf("read migration 0001: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply migration 0001: %v", err)
	}

	sql0002, err := os.ReadFile(filepath.Join(root, "migrations", "0002_add_is_system_to_aliases.up.sql"))
	if err != nil {
		t.Fatalf("read migration 0002: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql0002)); err != nil {
		t.Fatalf("apply migration 0002: %v", err)
	}

	// Seed domain & admin mailbox
	domainID := uuid.New()
	adminMailboxID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'dnstest.example', 'dnstest.example')`, domainID); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'admin', 'admin@dnstest.example', 'hash')`, adminMailboxID, domainID); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	aliasRepo := postgres.NewAliasRepository(pool)
	fakeDns := &e2eFakeDnsProvider{records: make(map[string]entity.DnsRecord)}

	uc := appusecase.NewSyncDnsRecordsUseCase(fakeDns, aliasRepo)

	input := appusecase.SyncDnsRecordsInput{
		DomainName:        "dnstest.example",
		MailHost:          "mail.dnstest.example",
		ServerIPv4:        "198.51.100.10",
		ServerIPv6:        "2001:db8::10",
		RsaDkimPubKey:     "rsaKeyString",
		EdDkimPubKey:      "edKeyString",
		TlsaHash:          "hashValue",
		DaneEnabled:       true,
		AdminEmailAddress: "admin@dnstest.example",
	}

	out, err := uc.Execute(ctx, input)
	if err != nil {
		t.Fatalf("SyncDnsRecords execute: %v", err)
	}

	if out.CreatedCount != 13 {
		t.Errorf("CreatedCount = %d, want 13", out.CreatedCount)
	}

	// Verify 4 system aliases in PostgreSQL (postmaster@, abuse@, dmarc@, tlsrpt@)
	var aliasCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM aliases WHERE domain_id = $1 AND is_system = true`, domainID).Scan(&aliasCount); err != nil || aliasCount != 4 {
		t.Fatalf("expected 4 system aliases in DB, got count=%d, err=%v", aliasCount, err)
	}
}

type e2eFakeDnsProvider struct {
	records map[string]entity.DnsRecord
}

func (f *e2eFakeDnsProvider) GetZoneID(_ context.Context, domainName string) (string, error) {
	return "zone_123", nil
}

func (f *e2eFakeDnsProvider) ListRecords(_ context.Context, _ string) ([]entity.DnsRecord, error) {
	var out []entity.DnsRecord
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

func (f *e2eFakeDnsProvider) CreateRecord(_ context.Context, _ string, record entity.DnsRecord) error {
	k := record.Type + ":" + record.Name
	f.records[k] = record
	return nil
}

func (f *e2eFakeDnsProvider) UpdateRecord(_ context.Context, _ string, record entity.DnsRecord) error {
	k := record.Type + ":" + record.Name
	f.records[k] = record
	return nil
}

func (f *e2eFakeDnsProvider) DeleteRecord(_ context.Context, _ string, recordID string) error {
	return nil
}
