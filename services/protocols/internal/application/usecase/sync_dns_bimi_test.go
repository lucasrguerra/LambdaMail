package usecase

import (
	"context"
	"strings"
	"testing"

	"lambdamail/protocols/internal/domain/entity"
)

func bimiInput(logo, cert string) SyncDnsRecordsInput {
	return SyncDnsRecordsInput{
		DomainName:  "example.test",
		MailHost:    "mail.example.test",
		ServerIPv4:  "192.0.2.1",
		BimiLogoURL: logo,
		BimiVmcURL:  cert,
	}
}

const (
	bimiName = "default._bimi.example.test"
	bimiLogo = "https://mail.example.test/.well-known/bimi/default.svg"
	bimiCert = "https://mail.example.test/.well-known/bimi/vmc.pem"
)

func bimiRecords(dns *fakeDnsProvider) []entity.DnsRecord { return dns.find("TXT", bimiName) }

// The gap this closes: the console knew the BIMI record, the reconciler did
// not, so storing a logo never put anything in the zone.
func TestSyncPublishesTheBimiRecord(t *testing.T) {
	dns := newFakeDnsProvider()
	uc := NewSyncDnsRecordsUseCase(dns, &fakeSystemAliasRepository{})

	out, err := uc.Execute(context.Background(), bimiInput(bimiLogo, ""))
	if err != nil {
		t.Fatal(err)
	}
	got := bimiRecords(dns)
	if len(got) != 1 || got[0].Value != "v=BIMI1; l="+bimiLogo+";" {
		t.Fatalf("records = %+v", got)
	}
	if !strings.HasPrefix(got[0].Comment, "LambdaMail") {
		t.Errorf("the record must carry the ownership comment, got %q", got[0].Comment)
	}
	if out.CreatedCount == 0 {
		t.Error("nothing reported as created")
	}

	// Idempotent
	out2, _ := uc.Execute(context.Background(), bimiInput(bimiLogo, ""))
	if out2.CreatedCount != 0 || out2.UpdatedCount != 0 || len(bimiRecords(dns)) != 1 {
		t.Errorf("second run changed something: created=%d updated=%d", out2.CreatedCount, out2.UpdatedCount)
	}
}

func TestSyncUpdatesTheBimiRecordWhenACertificateIsAdded(t *testing.T) {
	dns := newFakeDnsProvider()
	uc := NewSyncDnsRecordsUseCase(dns, &fakeSystemAliasRepository{})
	_, _ = uc.Execute(context.Background(), bimiInput(bimiLogo, ""))

	out, err := uc.Execute(context.Background(), bimiInput(bimiLogo, bimiCert))
	if err != nil {
		t.Fatal(err)
	}
	got := bimiRecords(dns)
	if len(got) != 1 || got[0].Value != "v=BIMI1; l="+bimiLogo+"; a="+bimiCert+";" {
		t.Fatalf("records = %+v", got)
	}
	if out.UpdatedCount != 1 {
		t.Errorf("updated = %d, want 1 (an update, not a second record)", out.UpdatedCount)
	}

	// And back, when the certificate is withheld
	_, _ = uc.Execute(context.Background(), bimiInput(bimiLogo, ""))
	if got := bimiRecords(dns); len(got) != 1 || strings.Contains(got[0].Value, "a=") {
		t.Fatalf("the certificate stayed advertised: %+v", got)
	}
}

func TestSyncRemovesTheBimiRecordItPublishedWhenTheLogoGoes(t *testing.T) {
	dns := newFakeDnsProvider()
	uc := NewSyncDnsRecordsUseCase(dns, &fakeSystemAliasRepository{})
	_, _ = uc.Execute(context.Background(), bimiInput(bimiLogo, ""))

	out, err := uc.Execute(context.Background(), bimiInput("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := bimiRecords(dns); len(got) != 0 {
		t.Fatalf("the record survived the logo: %+v", got)
	}
	if out.DeletedCount != 1 {
		t.Errorf("deleted = %d", out.DeletedCount)
	}
}

// A record the operator made by hand is theirs, even if it looks like ours.
func TestSyncLeavesAHandMadeBimiRecordAlone(t *testing.T) {
	dns := newFakeDnsProvider()
	dns.seed(entity.DnsRecord{Type: "TXT", Name: bimiName, Value: "v=BIMI1; l=https://cdn.example.test/logo.svg;", TTL: 1, Comment: "made by hand"})
	uc := NewSyncDnsRecordsUseCase(dns, &fakeSystemAliasRepository{})

	out, err := uc.Execute(context.Background(), bimiInput("", ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := bimiRecords(dns); len(got) != 1 || out.DeletedCount != 0 {
		t.Fatalf("a record this reconciler did not publish was touched: %+v deleted=%d", got, out.DeletedCount)
	}
}

func TestSyncPublishesNothingForADomainWithoutALogo(t *testing.T) {
	dns := newFakeDnsProvider()
	uc := NewSyncDnsRecordsUseCase(dns, &fakeSystemAliasRepository{})
	if _, err := uc.Execute(context.Background(), bimiInput("", "")); err != nil {
		t.Fatal(err)
	}
	if got := bimiRecords(dns); len(got) != 0 {
		t.Fatalf("records = %+v", got)
	}
}
