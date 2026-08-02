package usecase

import (
	"context"
	"strings"
	"testing"

	"lambdamail/protocols/internal/domain/entity"
)

func conflictTestInput() SyncDnsRecordsInput {
	return SyncDnsRecordsInput{
		DomainName:    "example.test",
		MailHost:      "mail.example.test",
		ServerIPv4:    "192.0.2.1",
		RsaDkimPubKey: "rsaKey",
		EdDkimPubKey:  "edKey",
	}
}

// PLAN.md section 7.5: pre-existing records that are not ours are never
// touched, only reported as conflicts. A third-party verification TXT sits at
// the zone apex under the same type and name as our SPF record, so a reconciler
// keyed only on type+name would overwrite it and break the other service.
func TestSyncDnsRecords_ForeignApexTxtIsPreserved(t *testing.T) {
	dns := newFakeDnsProvider()
	dns.seed(entity.DnsRecord{
		Type:  "TXT",
		Name:  "example.test",
		Value: "google-site-verification=abc123",
	})

	uc := NewSyncDnsRecordsUseCase(dns, nil)
	out, err := uc.Execute(context.Background(), conflictTestInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	apexTxt := dns.find("TXT", "example.test")
	if len(apexTxt) != 2 {
		t.Fatalf("expected the foreign TXT and our SPF to coexist, got %d records", len(apexTxt))
	}

	var foundForeign, foundSPF bool
	for _, r := range apexTxt {
		if r.Value == "google-site-verification=abc123" {
			foundForeign = true
		}
		if strings.HasPrefix(r.Value, "v=spf1") {
			foundSPF = true
		}
	}
	if !foundForeign {
		t.Error("the foreign verification TXT was destroyed")
	}
	if !foundSPF {
		t.Error("the SPF record was not created alongside the foreign apex TXT")
	}
	if out.ConflictCount != 0 {
		t.Errorf("a coexisting foreign TXT is not a conflict, got ConflictCount=%d", out.ConflictCount)
	}
}

// A name that admits only one value (the mail host A record) already pointing
// somewhere else is a genuine conflict: silently repointing it could take down
// an unrelated service.
func TestSyncDnsRecords_ForeignARecordIsReportedAsConflict(t *testing.T) {
	dns := newFakeDnsProvider()
	dns.seed(entity.DnsRecord{
		Type:    "A",
		Name:    "mail.example.test",
		Value:   "198.51.100.9",
		Comment: "managed by something else",
	})

	uc := NewSyncDnsRecordsUseCase(dns, nil)
	out, err := uc.Execute(context.Background(), conflictTestInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	records := dns.find("A", "mail.example.test")
	if len(records) != 1 || records[0].Value != "198.51.100.9" {
		t.Errorf("foreign A record was not left untouched: %+v", records)
	}
	if out.ConflictCount != 1 {
		t.Fatalf("ConflictCount = %d, want 1", out.ConflictCount)
	}
	if out.Status != SyncStatusPartial {
		t.Errorf("Status = %s, want %s", out.Status, SyncStatusPartial)
	}
	if !strings.Contains(out.Conflicts[0].String(), "198.51.100.9") {
		t.Errorf("conflict report does not name the blocking value: %s", out.Conflicts[0])
	}
}

// Our own records must still be updated in place when they drift.
func TestSyncDnsRecords_OwnRecordDriftIsCorrected(t *testing.T) {
	dns := newFakeDnsProvider()
	uc := NewSyncDnsRecordsUseCase(dns, nil)
	ctx := context.Background()

	if _, err := uc.Execute(ctx, conflictTestInput()); err != nil {
		t.Fatalf("initial Execute: %v", err)
	}

	for i, r := range dns.records {
		if r.Type == "A" && r.Name == "mail.example.test" {
			dns.records[i].Value = "203.0.113.7"
		}
	}

	out, err := uc.Execute(ctx, conflictTestInput())
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if out.UpdatedCount != 1 || out.ConflictCount != 0 {
		t.Errorf("updated=%d conflicts=%d, want updated=1 conflicts=0", out.UpdatedCount, out.ConflictCount)
	}
	if records := dns.find("A", "mail.example.test"); records[0].Value != "192.0.2.1" {
		t.Errorf("drifted A record was not corrected: got %q", records[0].Value)
	}
	if out.Status != SyncStatusVerified {
		t.Errorf("Status = %s, want %s", out.Status, SyncStatusVerified)
	}
}

type stubDnsVerifier struct {
	invisible map[string]bool
}

func (s stubDnsVerifier) VerifyRecord(_ context.Context, record entity.DnsRecord) (bool, string) {
	if s.invisible[record.Name] {
		return false, "no answer"
	}
	return true, ""
}

// PLAN.md section 7.5 verifies through public resolvers after writing. A
// record the provider accepted but nobody can resolve is drift, not success.
func TestSyncDnsRecords_UnresolvableRecordIsReportedAsDrift(t *testing.T) {
	dns := newFakeDnsProvider()
	uc := NewSyncDnsRecordsUseCase(dns, nil)
	uc.SetVerifier(stubDnsVerifier{invisible: map[string]bool{"mail.example.test": true}})

	out, err := uc.Execute(context.Background(), conflictTestInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if out.Status != SyncStatusDrift {
		t.Errorf("Status = %s, want DRIFT", out.Status)
	}
	if len(out.Unverified) == 0 {
		t.Fatal("the unresolvable record was not reported")
	}
	if !strings.Contains(out.Unverified[0], "mail.example.test") {
		t.Errorf("Unverified = %v, want it to name the record", out.Unverified)
	}
}

func TestSyncDnsRecords_AllRecordsVisibleIsVerified(t *testing.T) {
	dns := newFakeDnsProvider()
	uc := NewSyncDnsRecordsUseCase(dns, nil)
	uc.SetVerifier(stubDnsVerifier{})

	out, err := uc.Execute(context.Background(), conflictTestInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if out.Status != SyncStatusVerified {
		t.Errorf("Status = %s, want VERIFIED", out.Status)
	}
	if len(out.Unverified) != 0 {
		t.Errorf("Unverified = %v, want empty", out.Unverified)
	}
}
