package entity

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type TlsRptPolicy struct {
	PolicyType   string
	SuccessCount int
	FailureCount int
}

type TlsRptReport struct {
	OrganizationName string
	ReportID         string
	Domain           string
	DateRangeBegin   time.Time
	DateRangeEnd     time.Time
	Policies         []TlsRptPolicy
}

type tlsRptJsonReport struct {
	OrganizationName string `json:"organization-name"`
	ReportID         string `json:"report-id"`
	DateRange        struct {
		StartDatetime string `json:"start-datetime"`
		EndDatetime   string `json:"end-datetime"`
	} `json:"date-range"`
	Policies []struct {
		Policy struct {
			PolicyType string `json:"policy-type"`
			Domain     string `json:"domain"`
		} `json:"policy"`
		Summary struct {
			TotalSuccessfulSessionCount int `json:"total-successful-session-count"`
			TotalFailureSessionCount    int `json:"total-failure-session-count"`
		} `json:"summary"`
	} `json:"policies"`
}

// ParseTlsRptReport parses JSON or gzipped JSON TLS-RPT report.
func ParseTlsRptReport(payload []byte) (*TlsRptReport, error) {
	data := payload
	if isGzip(payload) {
		gz, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gz.Close()
		decompressed, err := io.ReadAll(gz)
		if err != nil {
			return nil, fmt.Errorf("decompress gzip: %w", err)
		}
		data = decompressed
	}

	var parsed tlsRptJsonReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal tls-rpt json: %w", err)
	}

	beginTime, _ := time.Parse(time.RFC3339, parsed.DateRange.StartDatetime)
	endTime, _ := time.Parse(time.RFC3339, parsed.DateRange.EndDatetime)

	domain := ""
	if len(parsed.Policies) > 0 {
		domain = parsed.Policies[0].Policy.Domain
	}

	report := &TlsRptReport{
		OrganizationName: parsed.OrganizationName,
		ReportID:         parsed.ReportID,
		Domain:           domain,
		DateRangeBegin:   beginTime,
		DateRangeEnd:     endTime,
	}

	for _, p := range parsed.Policies {
		report.Policies = append(report.Policies, TlsRptPolicy{
			PolicyType:   p.Policy.PolicyType,
			SuccessCount: p.Summary.TotalSuccessfulSessionCount,
			FailureCount: p.Summary.TotalFailureSessionCount,
		})
	}

	return report, nil
}
