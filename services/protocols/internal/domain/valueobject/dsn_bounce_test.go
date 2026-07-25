package valueobject

import (
	"strings"
	"testing"
)

func TestBuildDsnReport_PreventsBounceLoopForEmptySender(t *testing.T) {
	_, isLoop := BuildDsnReport(DsnActionFailed, "", "user@external.test", "msg123", "550 User unknown")
	if !isLoop {
		t.Errorf("expected loop prevention for empty sender")
	}

	_, isLoop = BuildDsnReport(DsnActionFailed, "<>", "user@external.test", "msg123", "550 User unknown")
	if !isLoop {
		t.Errorf("expected loop prevention for <> sender")
	}
}

func TestBuildDsnReport_GeneratesValidMIME(t *testing.T) {
	payload, isLoop := BuildDsnReport(DsnActionFailed, "sender@local.test", "recipient@remote.test", "msg123", "550 User unknown")
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
