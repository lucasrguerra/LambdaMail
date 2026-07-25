package valueobject

import (
	"testing"
)

func TestNewCleanScanResult(t *testing.T) {
	res := NewCleanScanResult()
	if res.Verdict != ScanVerdictClean {
		t.Errorf("verdict = %s, want CLEAN", res.Verdict)
	}
}

func TestNewVirusScanResult(t *testing.T) {
	res := NewVirusScanResult("EICAR-Test-Signature")
	if res.Verdict != ScanVerdictVirusReject || res.VirusName != "EICAR-Test-Signature" {
		t.Errorf("unexpected virus scan result: %+v", res)
	}
}
