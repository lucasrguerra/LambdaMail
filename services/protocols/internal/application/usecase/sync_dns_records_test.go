package usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"lambdamail/protocols/internal/domain/entity"
)

// fakeDnsProvider models a real zone: several records may share a type and
// name (multiple TXT at the apex), so records are held in a list keyed by id.
type fakeDnsProvider struct {
	records []entity.DnsRecord
	nextID  int
}

func newFakeDnsProvider() *fakeDnsProvider {
	return &fakeDnsProvider{}
}

// seed publishes a pre-existing record that this reconciler did not create.
func (f *fakeDnsProvider) seed(record entity.DnsRecord) {
	if record.ID == "" {
		f.nextID++
		record.ID = fmt.Sprintf("seed_%d", f.nextID)
	}
	f.records = append(f.records, record)
}

// find returns the records published under a type and name.
func (f *fakeDnsProvider) find(recType, name string) []entity.DnsRecord {
	var out []entity.DnsRecord
	for _, r := range f.records {
		if strings.EqualFold(r.Type, recType) && strings.EqualFold(r.Name, name) {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeDnsProvider) GetZoneID(_ context.Context, domainName string) (string, error) {
	return fmt.Sprintf("zone_%s", domainName), nil
}

func (f *fakeDnsProvider) ListRecords(_ context.Context, _ string) ([]entity.DnsRecord, error) {
	return append([]entity.DnsRecord(nil), f.records...), nil
}

func (f *fakeDnsProvider) CreateRecord(_ context.Context, _ string, record entity.DnsRecord) error {
	f.nextID++
	record.ID = fmt.Sprintf("id_%d", f.nextID)
	f.records = append(f.records, record)
	return nil
}

func (f *fakeDnsProvider) UpdateRecord(_ context.Context, _ string, record entity.DnsRecord) error {
	for i, r := range f.records {
		if r.ID == record.ID {
			f.records[i] = record
			return nil
		}
	}
	return fmt.Errorf("record %s not found", record.ID)
}

func (f *fakeDnsProvider) DeleteRecord(_ context.Context, _ string, recordID string) error {
	for i, r := range f.records {
		if r.ID == recordID {
			f.records = append(f.records[:i], f.records[i+1:]...)
			return nil
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

// The full desired state is the 13 numbered records of PLAN.md section 7.1
// plus the three client-autoconfiguration records of section 7.2.
const expectedRecordCount = 16

func TestSyncDnsRecordsUseCase_CreatesFullRecordSetAndAliases(t *testing.T) {
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

	if out.CreatedCount != expectedRecordCount {
		t.Errorf("CreatedCount = %d, want %d", out.CreatedCount, expectedRecordCount)
	}
	if !alias.ensured {
		t.Error("expected system aliases to be ensured")
	}

	// 2. Second Sync (idempotent) -> Unchanged 13 records
	out2, err := uc.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute 2 failed: %v", err)
	}

	if out2.UnchangedCount != expectedRecordCount || out2.CreatedCount != 0 || out2.UpdatedCount != 0 {
		t.Errorf("expected %d unchanged, got created=%d, updated=%d, unchanged=%d", expectedRecordCount, out2.CreatedCount, out2.UpdatedCount, out2.UnchangedCount)
	}
}
