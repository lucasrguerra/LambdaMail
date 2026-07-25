package entity

import (
	"testing"
)

func TestBuild13DnsRecordSpecs_GeneratesMandatoryRecords(t *testing.T) {
	specs := Build13DnsRecordSpecs(
		"example.test",
		"mail.example.test",
		"192.0.2.1",
		"2001:db8::1",
		"rsaPubKeyBase64",
		"edPubKeyBase64",
		"hash123",
		true,
	)

	// Should contain 13 records (11 mandatory + 1 AAAA + 1 TLSA)
	if len(specs) != 13 {
		t.Fatalf("expected 13 records, got %d", len(specs))
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
