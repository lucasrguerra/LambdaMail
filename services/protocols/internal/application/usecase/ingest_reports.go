package usecase

import (
	"context"
	"fmt"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

type IngestReportsUseCase struct {
	reportRepo port.ReportRepository
}

func NewIngestReportsUseCase(reportRepo port.ReportRepository) *IngestReportsUseCase {
	return &IngestReportsUseCase{reportRepo: reportRepo}
}

func (uc *IngestReportsUseCase) IngestDmarc(ctx context.Context, payload []byte) (*entity.DmarcReport, error) {
	report, err := entity.ParseDmarcXmlReport(payload)
	if err != nil {
		return nil, fmt.Errorf("parse dmarc report: %w", err)
	}

	if uc.reportRepo != nil {
		if err := uc.reportRepo.SaveDmarcReport(ctx, report); err != nil {
			return nil, fmt.Errorf("save dmarc report: %w", err)
		}
	}

	return report, nil
}

func (uc *IngestReportsUseCase) IngestTlsRpt(ctx context.Context, payload []byte) (*entity.TlsRptReport, error) {
	report, err := entity.ParseTlsRptReport(payload)
	if err != nil {
		return nil, fmt.Errorf("parse tls-rpt report: %w", err)
	}

	if uc.reportRepo != nil {
		if err := uc.reportRepo.SaveTlsRptReport(ctx, report); err != nil {
			return nil, fmt.Errorf("save tls-rpt report: %w", err)
		}
	}

	return report, nil
}
