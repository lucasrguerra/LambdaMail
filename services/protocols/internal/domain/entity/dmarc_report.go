package entity

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"time"
)

type DmarcRecord struct {
	SourceIP    string
	Count       int
	Disposition string
	DKIMResult  string
	SPFResult   string
	HeaderFrom  string
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
			SourceIP        string `xml:"source_ip"`
			Count           int    `xml:"count"`
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

// ParseDmarcXmlReport parses a DMARC aggregate report: raw XML, gzipped XML,
// or XML inside a zip archive.
//
// Zip matters as much as gzip. Microsoft and Yahoo send .zip, and between them
// they account for a large share of the reports any domain receives; handling
// gzip alone meant those reports never parsed at all.
func ParseDmarcXmlReport(payload []byte) (*DmarcReport, error) {
	data := payload
	switch {
	case isGzip(payload):
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
	case isZip(payload):
		decompressed, err := firstFileInZip(payload)
		if err != nil {
			return nil, err
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

// isZip reports whether the payload begins with a local file header. The
// empty-archive and spanned-archive signatures are deliberately not accepted:
// neither carries a report.
func isZip(payload []byte) bool {
	return len(payload) >= 4 && payload[0] == 'P' && payload[1] == 'K' &&
		payload[2] == 0x03 && payload[3] == 0x04
}

// firstFileInZip returns the contents of the first regular file in the
// archive. An aggregate report is one XML document per archive, so there is
// nothing to choose between; directories are skipped so an archive that
// carries one cannot be mistaken for an empty one.
func firstFileInZip(payload []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("zip reader: %w", err)
	}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s in zip: %w", file.Name, err)
		}
		defer handle.Close()
		// Bounded so a zip bomb cannot exhaust this process: an aggregate
		// report is XML in the tens of kilobytes, and 64 MiB is far past any
		// legitimate one.
		decompressed, err := io.ReadAll(io.LimitReader(handle, 64<<20))
		if err != nil {
			return nil, fmt.Errorf("decompress %s in zip: %w", file.Name, err)
		}
		return decompressed, nil
	}
	return nil, fmt.Errorf("zip archive contains no report")
}
