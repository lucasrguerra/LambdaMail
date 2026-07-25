package usecase

import (
	"context"
	"testing"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/valueobject"
)

type mockScanner struct {
	res *valueobject.ScanResult
	err error
}

func (m *mockScanner) Scan(_ context.Context, _ port.ScanInput) (*valueobject.ScanResult, error) {
	return m.res, m.err
}

func TestScanningPipeline_VirusRejectsImmediately(t *testing.T) {
	s1 := &mockScanner{res: valueobject.NewVirusScanResult("EICAR-Test-Signature")}
	s2 := &mockScanner{res: valueobject.NewCleanScanResult()}

	pipeline := NewScanningPipeline(s1, s2)
	res, err := pipeline.Scan(context.Background(), port.ScanInput{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if res.Verdict != valueobject.ScanVerdictVirusReject || res.VirusName != "EICAR-Test-Signature" {
		t.Errorf("unexpected verdict: %+v", res)
	}
}

func TestScanningPipeline_AggregatesJunkVerdict(t *testing.T) {
	s1 := &mockScanner{res: valueobject.NewCleanScanResult()}
	junkRes := valueobject.NewCleanScanResult()
	junkRes.Verdict = valueobject.ScanVerdictSpamJunk
	junkRes.Score = 7.5
	s2 := &mockScanner{res: junkRes}

	pipeline := NewScanningPipeline(s1, s2)
	res, err := pipeline.Scan(context.Background(), port.ScanInput{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if res.Verdict != valueobject.ScanVerdictSpamJunk || res.Score != 7.5 {
		t.Errorf("unexpected junk result: %+v", res)
	}
}
