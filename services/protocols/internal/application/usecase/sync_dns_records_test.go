package usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"lambdamail/protocols/internal/domain/entity"
)

type fakeDnsProvider struct {
	records map[string]entity.DnsRecord // key -> record
}

func newFakeDnsProvider() *fakeDnsProvider {
	return &fakeDnsProvider{records: make(map[string]entity.DnsRecord)}
}

func (f *fakeDnsProvider) GetZoneID(_ context.Context, domainName string) (string, error) {
	return fmt.Sprintf("zone_%s", domainName), nil
}

func (f *fakeDnsProvider) ListRecords(_ context.Context, _ string) ([]entity.DnsRecord, error) {
	var out []entity.DnsRecord
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeDnsProvider) CreateRecord(_ context.Context, _ string, record entity.DnsRecord) error {
	key := fmt.Sprintf("%s:%s", strings.ToUpper(record.Type), strings.ToLower(record.Name))
	record.ID = fmt.Sprintf("id_%s", key)
	f.records[key] = record
	return nil
}

func (f *fakeDnsProvider) UpdateRecord(_ context.Context, _ string, record entity.DnsRecord) error {
	key := fmt.Sprintf("%s:%s", strings.ToUpper(record.Type), strings.ToLower(record.Name))
	f.records[key] = record
	return nil
}

func (f *fakeDnsProvider) DeleteRecord(_ context.Context, _ string, recordID string) error {
	for k, r := range f.records {
		if r.ID == recordID {
			delete(f.records, k)
			break
		}
	}
	return nil
}

type fakeSystemAliasRepository struct {
	ensured bool
}

func (f *fakeSystemAliasRepository) EnsureSystemAliases(_ context.Context, _ string, _ string) error {
	f.ensured = true
	return nil
}

func TestSyncDnsRecordsUseCase_Creates13RecordsAndAliases(t *testing.T) {
	dns := newFakeDnsProvider()
	alias := &fakeSystemAliasRepository{}
	uc := NewSyncDnsRecordsUseCase(dns, alias)

	ctx := context.Background()
	input := SyncDnsRecordsInput{
		DomainName:        "example.test",
		MailHost:          "mail.example.test",
		ServerIPv4:        "192.0.2.1",
		ServerIPv6:        "2001:db8::1",
		RsaDkimPubKey:     "rsaKey",
		EdDkimPubKey:      "edKey",
		TlsaHash:          "hash123",
		DaneEnabled:       true,
		AdminEmailAddress: "admin@example.test",
	}

	// 1. Initial Sync -> Creates 13 records
	out, err := uc.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if out.CreatedCount != 13 {
		t.Errorf("CreatedCount = %d, want 13", out.CreatedCount)
	}
	if !alias.ensured {
		t.Error("expected system aliases to be ensured")
	}

	// 2. Second Sync (idempotent) -> Unchanged 13 records
	out2, err := uc.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute 2 failed: %v", err)
	}

	if out2.UnchangedCount != 13 || out2.CreatedCount != 0 || out2.UpdatedCount != 0 {
		t.Errorf("expected 13 unchanged, got created=%d, updated=%d, unchanged=%d", out2.CreatedCount, out2.UpdatedCount, out2.UnchangedCount)
	}
}
