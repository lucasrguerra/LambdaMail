package entity

import "testing"

func TestTLSPolicy_Effective_DANETakesPrecedenceOverMTASTS(t *testing.T) {
	// RFC 8461 section 2, restated in PLAN.md section 5: DANE wins when both are present.
	p := NewTLSPolicy(true, true)
	if p.Effective() != "dane" {
		t.Errorf("Effective() = %q, want %q when both DANE and MTA-STS are available", p.Effective(), "dane")
	}
}

func TestTLSPolicy_Effective_FallsBackToMTASTSWithoutDANE(t *testing.T) {
	p := NewTLSPolicy(false, true)
	if p.Effective() != "mta-sts" {
		t.Errorf("Effective() = %q, want %q", p.Effective(), "mta-sts")
	}
}

func TestTLSPolicy_Effective_FallsBackToOpportunisticWithNeither(t *testing.T) {
	p := NewTLSPolicy(false, false)
	if p.Effective() != "opportunistic" {
		t.Errorf("Effective() = %q, want %q", p.Effective(), "opportunistic")
	}
}
