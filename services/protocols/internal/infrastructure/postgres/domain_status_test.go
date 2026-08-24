package postgres

import "testing"

// dns_status is constrained by the schema to a fixed set. The verifier answers
// with a wider vocabulary, and writing one of the extra words fails the check
// constraint - the update is rejected and the console goes on showing the
// stale badge this was written to fix.
func TestColumnStatus_OnlyEverProducesAcceptedValues(t *testing.T) {
	allowed := map[string]bool{
		"PENDING": true, "VERIFIED": true, "PARTIAL": true, "DRIFT": true, "ERROR": true,
	}

	for _, in := range []string{"VERIFIED", "PARTIAL", "MISSING", "UNKNOWN", "", "something new"} {
		got := columnStatus(in)
		if !allowed[got] {
			t.Errorf("columnStatus(%q) = %q, which the check constraint rejects", in, got)
		}
	}

	if got := columnStatus("MISSING"); got != "ERROR" {
		t.Errorf("MISSING became %q; nothing resolved, so it is an error, not a pending check", got)
	}
	if got := columnStatus("VERIFIED"); got != "VERIFIED" {
		t.Errorf("VERIFIED became %q", got)
	}
}
