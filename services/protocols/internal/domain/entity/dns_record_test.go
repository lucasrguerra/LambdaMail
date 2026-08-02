package entity

import (
	"testing"
)

func TestBuildDnsRecordSpecs_GeneratesMandatoryRecords(t *testing.T) {
	specs := BuildDnsRecordSpecs(DnsRecordSpec{
		DomainName:    "example.test",
		MailHost:      "mail.example.test",
		ServerIPv4:    "192.0.2.1",
		ServerIPv6:    "2001:db8::1",
		RsaDkimPubKey: "rsaPubKeyBase64",
		EdDkimPubKey:  "edPubKeyBase64",
		TlsaHashes:    []string{"hash123"},
		DaneEnabled:   true,
	})

	// PLAN.md section 7.1 numbers 13 records (11 always-on, plus AAAA when
	// IPv6 is configured and TLSA when DANE is on). Section 7.2 adds three
	// unnumbered client-autoconfiguration records: the _pop3s SRV, the
	// autoconfig CNAME and the _autodiscover SRV.
	const numberedRecords, convenienceRecords = 13, 3
	if len(specs) != numberedRecords+convenienceRecords {
		t.Fatalf("expected %d records, got %d", numberedRecords+convenienceRecords, len(specs))
	}

	// The HTTPS endpoints of section 7.3 are unreachable without the names
	// that point at them.
	for _, required := range []struct{ recType, name string }{
		{"CNAME", "autoconfig.example.test"},
		{"SRV", "_pop3s._tcp.example.test"},
		{"SRV", "_autodiscover._tcp.example.test"},
	} {
		found := false
		for _, r := range specs {
			if r.Type == required.recType && r.Name == required.name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s %s not found in specs", required.recType, required.name)
		}
	}

	// Verify MX
	foundMX := false
	for _, r := range specs {
		if r.Type == "MX" && r.Name == "example.test" && r.Value == "mail.example.test" {
			foundMX = true
			if r.Priority == nil || *r.Priority != 10 {
				t.Errorf("MX priority = %v, want 10", r.Priority)
			}
		}
	}
	if !foundMX {
		t.Error("MX record not found in specs")
	}

	// Verify DMARC
	foundDmarc := false
	for _, r := range specs {
		if r.Type == "TXT" && r.Name == "_dmarc.example.test" {
			foundDmarc = true
		}
	}
	if !foundDmarc {
		t.Error("DMARC record not found in specs")
	}
}
