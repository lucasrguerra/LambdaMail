package entity

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"time"
)

type DmarcRecord struct {
	SourceIP        string
	Count           int
	Disposition     string
	DKIMResult      string
	SPFResult       string
	HeaderFrom      string
}

type DmarcReport struct {
	OrgName        string
	ReportID       string
	Domain         string
	DateRangeBegin time.Time
	DateRangeEnd   time.Time
	Records        []DmarcRecord
}

type dmarcXmlReport struct {
	ReportMetadata struct {
		OrgName   string `xml:"org_name"`
		ReportID  string `xml:"report_id"`
		DateRange struct {
			Begin int64 `xml:"begin"`
			End   int64 `xml:"end"`
		} `xml:"date_range"`
	} `xml:"report_metadata"`
	PolicyPublished struct {
		Domain string `xml:"domain"`
	} `xml:"policy_published"`
	Record []struct {
		Row struct {
			SourceIP string `xml:"source_ip"`
			Count    int    `xml:"count"`
			PolicyEvaluated struct {
				Disposition string `xml:"disposition"`
				DKIM        string `xml:"dkim"`
				SPF         string `xml:"spf"`
			} `xml:"policy_evaluated"`
		} `xml:"row"`
		Identifiers struct {
			HeaderFrom string `xml:"header_from"`
		} `xml:"identifiers"`
	} `xml:"record"`
}

// ParseDmarcXmlReport parses raw XML or gzipped XML DMARC aggregate report.
func ParseDmarcXmlReport(payload []byte) (*DmarcReport, error) {
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

	var parsed dmarcXmlReport
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal dmarc xml: %w", err)
	}

	report := &DmarcReport{
		OrgName:        parsed.ReportMetadata.OrgName,
		ReportID:       parsed.ReportMetadata.ReportID,
		Domain:         parsed.PolicyPublished.Domain,
		DateRangeBegin: time.Unix(parsed.ReportMetadata.DateRange.Begin, 0),
		DateRangeEnd:   time.Unix(parsed.ReportMetadata.DateRange.End, 0),
	}

	for _, r := range parsed.Record {
		report.Records = append(report.Records, DmarcRecord{
			SourceIP:    r.Row.SourceIP,
			Count:       r.Row.Count,
			Disposition: r.Row.PolicyEvaluated.Disposition,
			DKIMResult:  r.Row.PolicyEvaluated.DKIM,
			SPFResult:   r.Row.PolicyEvaluated.SPF,
			HeaderFrom:  r.Identifiers.HeaderFrom,
		})
	}

	return report, nil
}

func isGzip(payload []byte) bool {
	return len(payload) >= 2 && payload[0] == 0x1f && payload[1] == 0x8b
}
