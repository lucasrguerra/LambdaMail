package valueobject

import (
	"strings"
	"testing"
)

func TestBuildDsnReport_PreventsBounceLoopForEmptySender(t *testing.T) {
	_, isLoop := BuildDsnReport(DsnActionFailed, "mail.local.test", "", "user@external.test", "msg123", "550 User unknown")
	if !isLoop {
		t.Errorf("expected loop prevention for empty sender")
	}

	_, isLoop = BuildDsnReport(DsnActionFailed, "mail.local.test", "<>", "user@external.test", "msg123", "550 User unknown")
	if !isLoop {
		t.Errorf("expected loop prevention for <> sender")
	}
}

func TestBuildDsnReport_GeneratesValidMIME(t *testing.T) {
	payload, isLoop := BuildDsnReport(DsnActionFailed, "mail.local.test", "sender@local.test", "recipient@remote.test", "msg123", "550 User unknown")
	if isLoop || payload == nil {
		t.Fatalf("unexpected loop signal for valid sender")
	}

	raw := string(payload)
	if !strings.Contains(raw, "Content-Type: multipart/report; report-type=delivery-status") {
		t.Errorf("missing multipart/report header: %s", raw)
	}
	if !strings.Contains(raw, "Action: failed") {
		t.Errorf("missing Action: failed: %s", raw)
	}
	if !strings.Contains(raw, "Diagnostic-Code: smtp; 550 User unknown") {
		t.Errorf("missing Diagnostic-Code: %s", raw)
	}
}

// A notification whose From and Reporting-MTA name a domain that does not
// resolve is itself undeliverable, which loses the very message the sender
// most needs to see.
func TestBuildDsnReport_UsesTheConfiguredMailHost(t *testing.T) {
	payload, _ := BuildDsnReport(DsnActionFailed, "mail.example.test", "sender@example.test", "recipient@remote.test", "job-1", "550 User unknown")

	raw := string(payload)
	if !strings.Contains(raw, "From: Mail Delivery Subsystem <postmaster@mail.example.test>") {
		t.Errorf("From does not use the configured mail host: %s", raw)
	}
	if !strings.Contains(raw, "Reporting-MTA: dns; mail.example.test") {
		t.Errorf("Reporting-MTA does not use the configured mail host: %s", raw)
	}
	if strings.Contains(raw, "lambdamail.local") {
		t.Errorf("the placeholder host leaked into the report: %s", raw)
	}
}

// RFC 3834 section 5: without Auto-Submitted a vacation responder answers the
// bounce and the two loop against each other.
func TestBuildDsnReport_MarksItselfAutomatic(t *testing.T) {
	payload, _ := BuildDsnReport(DsnActionDelayed, "mail.example.test", "sender@example.test", "recipient@remote.test", "job-1", "451 deferred")

	if !strings.Contains(string(payload), "Auto-Submitted: auto-replied") {
		t.Errorf("the report is not marked as automatic: %s", payload)
	}
}
