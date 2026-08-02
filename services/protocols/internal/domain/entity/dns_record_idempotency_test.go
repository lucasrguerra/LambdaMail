package entity

import (
	"testing"
)

func buildTestSpecs() []DnsRecord {
	return BuildDnsRecordSpecs(DnsRecordSpec{
		DomainName:    "example.test",
		MailHost:      "mail.example.test",
		ServerIPv4:    "192.0.2.1",
		RsaDkimPubKey: "rsaPubKeyBase64",
		EdDkimPubKey:  "edPubKeyBase64",
	})
}

func findRecord(t *testing.T, specs []DnsRecord, recType, name string) DnsRecord {
	t.Helper()
	for _, r := range specs {
		if r.Type == recType && r.Name == name {
			return r
		}
	}
	t.Fatalf("record %s %s not found", recType, name)
	return DnsRecord{}
}

// PLAN.md section 7.5 guarantees the sync is fully idempotent (running it 100
// times equals running it once). A wall-clock derived MTA-STS policy id breaks
// that: every reconcile would rewrite the record and force every sender to
// re-fetch the policy.
func TestBuildDnsRecordSpecs_MtaStsIdIsStableAcrossCalls(t *testing.T) {
	first := findRecord(t, buildTestSpecs(), "TXT", "_mta-sts.example.test")
	second := findRecord(t, buildTestSpecs(), "TXT", "_mta-sts.example.test")

	if first.Value != second.Value {
		t.Errorf("MTA-STS id is not stable across calls: %q vs %q", first.Value, second.Value)
	}
}

// The id must still change when the policy content changes, otherwise senders
// keep serving a stale cached policy (RFC 8461 section 3.1).
func TestBuildDnsRecordSpecs_MtaStsIdChangesWithPolicy(t *testing.T) {
	base := findRecord(t, buildTestSpecs(), "TXT", "_mta-sts.example.test")

	otherHost := BuildDnsRecordSpecs(DnsRecordSpec{
		DomainName:    "example.test",
		MailHost:      "mx2.example.test",
		ServerIPv4:    "192.0.2.1",
		RsaDkimPubKey: "rsaPubKeyBase64",
		EdDkimPubKey:  "edPubKeyBase64",
	})
	changed := findRecord(t, otherHost, "TXT", "_mta-sts.example.test")

	if base.Value == changed.Value {
		t.Errorf("MTA-STS id did not change when the mail host changed: %q", base.Value)
	}
}

// A drifted MX priority is a real outage (mail routed to the wrong host) and
// must be detected by the reconciler's comparison.
func TestDnsRecord_EqualsNormalized_DetectsPriorityDrift(t *testing.T) {
	ten, twenty := 10, 20
	a := DnsRecord{Type: "MX", Name: "example.test", Value: "mail.example.test", Priority: &ten}
	b := DnsRecord{Type: "MX", Name: "example.test", Value: "mail.example.test", Priority: &twenty}

	if a.EqualsNormalized(b) {
		t.Error("expected records with different MX priority to compare unequal")
	}
	if !a.EqualsNormalized(DnsRecord{Type: "MX", Name: "example.test", Value: "mail.example.test", Priority: &ten}) {
		t.Error("expected records with identical MX priority to compare equal")
	}
}
