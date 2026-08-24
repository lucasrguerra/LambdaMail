package entity

import (
	"strings"
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

// An empty "p=" is a revocation, not an absent key (RFC 6376 section 3.6.1).
// Publishing one for a domain that simply has not provisioned keys yet tells
// every verifier that the selector's signatures are invalid - and on a domain
// whose DMARC policy is strict, that quarantines all of its own outbound mail.
func TestDkimRecordsAreOmittedWhenThereIsNoKey(t *testing.T) {
	records := BuildDnsRecordSpecs(DnsRecordSpec{
		DomainName: "example.test",
		MailHost:   "mail.example.test",
		ServerIPv4: "198.51.100.10",
	})

	for _, r := range records {
		if strings.Contains(r.Name, "_domainkey") {
			t.Errorf("published a DKIM record with no key: %s = %q", r.Name, r.Value)
		}
		if strings.Contains(r.Value, "p=\"") || strings.HasSuffix(r.Value, "p=") {
			t.Errorf("published an empty public key: %s = %q", r.Name, r.Value)
		}
	}
}

func TestDkimRecordsArePublishedWhenKeysExist(t *testing.T) {
	records := BuildDnsRecordSpecs(DnsRecordSpec{
		DomainName:    "example.test",
		MailHost:      "mail.example.test",
		ServerIPv4:    "198.51.100.10",
		RsaDkimPubKey: "MIIBIjANBgkq",
		EdDkimPubKey:  "11qYAYKxCrfV",
	})

	var rsa, ed bool
	for _, r := range records {
		if r.Name == "default._domainkey.example.test" && strings.Contains(r.Value, "MIIBIjANBgkq") {
			rsa = true
		}
		if r.Name == "default-ed._domainkey.example.test" && strings.Contains(r.Value, "11qYAYKxCrfV") {
			ed = true
		}
	}
	if !rsa {
		t.Error("the RSA DKIM record was not published despite a key being present")
	}
	if !ed {
		t.Error("the Ed25519 DKIM record was not published despite a key being present")
	}
}

// One key present and the other absent must publish exactly one record: an
// Ed25519 rollout should not revoke the RSA selector that is still signing.
func TestOnlyTheAvailableDkimKeyIsPublished(t *testing.T) {
	records := BuildDnsRecordSpecs(DnsRecordSpec{
		DomainName:    "example.test",
		MailHost:      "mail.example.test",
		RsaDkimPubKey: "MIIBIjANBgkq",
	})

	for _, r := range records {
		if strings.Contains(r.Name, "default-ed._domainkey") {
			t.Errorf("published an Ed25519 record with no key: %q", r.Value)
		}
	}
}

// A second domain served by the same server does not own the mail host.
//
// Every domain's spec included an A and an AAAA for the mail host, so
// reconciling cienciaembarcada.com.br tried to create records for
// mail.lucasrguerra.dev.br - a name in somebody else's zone. Cloudflare
// answered "An identical record already exists" and the console showed the
// operator two errors about a domain that was in fact perfectly configured.
//
// The address records belong to whichever zone actually contains the host.
func TestBuildDnsRecordSpecs_SkipsMailHostAddressesOutsideTheDomain(t *testing.T) {
	specs := BuildDnsRecordSpecs(DnsRecordSpec{
		DomainName:  "cienciaembarcada.test",
		MailHost:    "mail.lucasrguerra.test",
		ServerIPv4:  "192.0.2.1",
		ServerIPv6:  "2001:db8::1",
		DaneEnabled: true,
		TlsaHashes:  []string{"hash123"},
	})

	for _, r := range specs {
		if r.Type == "A" || r.Type == "AAAA" || r.Type == "TLSA" {
			t.Errorf("spec publishes %s for %q, which is not inside cienciaembarcada.test",
				r.Type, r.Name)
		}
	}

	// The records that point AT the mail host are still the domain's own and
	// must stay, or the domain stops receiving mail.
	var mx bool
	for _, r := range specs {
		if r.Type == "MX" && r.Value == "mail.lucasrguerra.test" {
			mx = true
		}
	}
	if !mx {
		t.Error("the domain lost its MX; it would stop receiving mail")
	}
}

// The ordinary case must keep them: a domain whose mail host is its own
// subdomain still needs the address records published.
func TestBuildDnsRecordSpecs_KeepsMailHostAddressesInsideTheDomain(t *testing.T) {
	specs := BuildDnsRecordSpecs(DnsRecordSpec{
		DomainName: "example.test",
		MailHost:   "mail.example.test",
		ServerIPv4: "192.0.2.1",
		ServerIPv6: "2001:db8::1",
	})

	var a, aaaa bool
	for _, r := range specs {
		if r.Type == "A" && r.Name == "mail.example.test" {
			a = true
		}
		if r.Type == "AAAA" && r.Name == "mail.example.test" {
			aaaa = true
		}
	}
	if !a || !aaaa {
		t.Errorf("lost the mail host addresses (A=%v AAAA=%v)", a, aaaa)
	}
}
