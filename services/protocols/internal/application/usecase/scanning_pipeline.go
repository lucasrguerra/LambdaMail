package usecase

import (
	"context"
	"fmt"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/valueobject"
)

type ScanningPipeline struct {
	scanners []port.ContentScanner
}

func NewScanningPipeline(scanners ...port.ContentScanner) *ScanningPipeline {
	return &ScanningPipeline{scanners: scanners}
}

func (p *ScanningPipeline) Scan(ctx context.Context, input port.ScanInput) (*valueobject.ScanResult, error) {
	finalResult := valueobject.NewCleanScanResult()

	for _, scanner := range p.scanners {
		if scanner == nil {
			continue
		}
		res, err := scanner.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scanner failed: %w", err)
		}
		if res == nil {
			continue
		}

		// Fail fast on Virus Reject, Spam Reject, or Greylist
		if res.Verdict == valueobject.ScanVerdictVirusReject ||
			res.Verdict == valueobject.ScanVerdictSpamReject ||
			res.Verdict == valueobject.ScanVerdictGreylist {
			return res, nil
		}

		if res.Verdict == valueobject.ScanVerdictSpamJunk {
			finalResult.Verdict = valueobject.ScanVerdictSpamJunk
		}

		if res.Score != 0 {
			finalResult.Score = res.Score
			finalResult.RequiredScore = res.RequiredScore
		}
		for k, v := range res.HeadersToAdd {
			finalResult.HeadersToAdd[k] = v
		}
	}

	return finalResult, nil
}
