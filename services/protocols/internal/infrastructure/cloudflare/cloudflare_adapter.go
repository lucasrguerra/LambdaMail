package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

const (
	// rateLimitMaxAttempts bounds the 429 retry loop so a throttled zone
	// delays the reconcile instead of blocking it forever.
	rateLimitMaxAttempts = 4
	rateLimitMaxBackoff  = 30 * time.Second
	recordsPerPage       = 100
)

type CloudflareAdapter struct {
	apiToken   string
	baseURL    string
	httpClient *http.Client
}

func NewCloudflareAdapter(apiToken string) *CloudflareAdapter {
	return &CloudflareAdapter{
		apiToken: apiToken,
		baseURL:  "https://api.cloudflare.com/client/v4",
		// Without a timeout a stalled API call would hold the reconcile - and
		// on the periodic drift job, the goroutine - open indefinitely.
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type cfError struct {
	Message string `json:"message"`
}

type cfResultInfo struct {
	Page       int `json:"page"`
	TotalPages int `json:"total_pages"`
}

type cfZoneResponse struct {
	Success bool `json:"success"`
	Result  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
	Errors []cfError `json:"errors"`
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content,omitempty"`
	TTL     int    `json:"ttl"`
	// Pointers so that "absent" and "explicitly false" stay distinguishable:
	// Cloudflare applies its own default for an omitted proxied flag, and a
	// proxied mail host silently breaks SMTP (PLAN.md section 7.1).
	Priority *int  `json:"priority,omitempty"`
	Proxied  *bool `json:"proxied,omitempty"`
	// Data carries the structured form Cloudflare requires for SRV and TLSA;
	// those types reject a flat content string.
	Data    map[string]any `json:"data,omitempty"`
	Comment string         `json:"comment,omitempty"`
}

type cfRecordResponse struct {
	Success    bool         `json:"success"`
	Result     []cfRecord   `json:"result"`
	ResultInfo cfResultInfo `json:"result_info"`
	Errors     []cfError    `json:"errors"`
}

type cfSingleRecordResponse struct {
	Success bool      `json:"success"`
	Result  cfRecord  `json:"result"`
	Errors  []cfError `json:"errors"`
}

func (a *CloudflareAdapter) GetZoneID(ctx context.Context, domainName string) (string, error) {
	reqURL := fmt.Sprintf("%s/zones?name=%s", a.baseURL, url.QueryEscape(domainName))

	var data cfZoneResponse
	if err := a.doJSON(ctx, http.MethodGet, reqURL, nil, &data, func() []cfError { return data.Errors }); err != nil {
		return "", err
	}

	if !data.Success || len(data.Result) == 0 {
		return "", fmt.Errorf("cloudflare: zone %q not found or token lacks Zone:Read on it", domainName)
	}

	return data.Result[0].ID, nil
}

func (a *CloudflareAdapter) ListRecords(ctx context.Context, zoneID string) ([]entity.DnsRecord, error) {
	var out []entity.DnsRecord

	for page := 1; ; page++ {
		reqURL := fmt.Sprintf("%s/zones/%s/dns_records?per_page=%d&page=%d", a.baseURL, zoneID, recordsPerPage, page)

		var data cfRecordResponse
		if err := a.doJSON(ctx, http.MethodGet, reqURL, nil, &data, func() []cfError { return data.Errors }); err != nil {
			return nil, err
		}
		if !data.Success {
			return nil, fmt.Errorf("cloudflare: listing records for zone %s failed", zoneID)
		}

		for _, r := range data.Result {
			rec := entity.DnsRecord{
				ID:       r.ID,
				Type:     r.Type,
				Name:     r.Name,
				Value:    r.Content,
				TTL:      r.TTL,
				Priority: r.Priority,
				Comment:  r.Comment,
			}
			if r.Proxied != nil {
				rec.Proxied = *r.Proxied
			}
			// Cloudflare returns SRV and TLSA with content already rendered,
			// but older zones and some responses only populate data; fall back
			// to it so a structured record is not seen as an empty value and
			// rewritten on every reconcile.
			if rec.Value == "" && len(r.Data) > 0 {
				rec.Value = renderStructuredValue(r.Type, r.Data)
			}
			out = append(out, rec)
		}

		// Zones larger than one page would otherwise look like they are
		// missing records, and the reconciler would try to re-create them.
		if data.ResultInfo.TotalPages <= page || len(data.Result) == 0 {
			break
		}
	}

	return out, nil
}

func (a *CloudflareAdapter) CreateRecord(ctx context.Context, zoneID string, record entity.DnsRecord) error {
	reqURL := fmt.Sprintf("%s/zones/%s/dns_records", a.baseURL, zoneID)

	var data cfSingleRecordResponse
	if err := a.doJSON(ctx, http.MethodPost, reqURL, toCfRecord(record), &data, func() []cfError { return data.Errors }); err != nil {
		return fmt.Errorf("create %s %s: %w", record.Type, record.Name, err)
	}
	if !data.Success {
		return fmt.Errorf("cloudflare: create %s %s failed", record.Type, record.Name)
	}
	return nil
}

func (a *CloudflareAdapter) UpdateRecord(ctx context.Context, zoneID string, record entity.DnsRecord) error {
	reqURL := fmt.Sprintf("%s/zones/%s/dns_records/%s", a.baseURL, zoneID, record.ID)

	var data cfSingleRecordResponse
	if err := a.doJSON(ctx, http.MethodPut, reqURL, toCfRecord(record), &data, func() []cfError { return data.Errors }); err != nil {
		return fmt.Errorf("update %s %s: %w", record.Type, record.Name, err)
	}
	if !data.Success {
		return fmt.Errorf("cloudflare: update %s %s failed", record.Type, record.Name)
	}
	return nil
}

func (a *CloudflareAdapter) DeleteRecord(ctx context.Context, zoneID string, recordID string) error {
	reqURL := fmt.Sprintf("%s/zones/%s/dns_records/%s", a.baseURL, zoneID, recordID)

	var data cfSingleRecordResponse
	if err := a.doJSON(ctx, http.MethodDelete, reqURL, nil, &data, func() []cfError { return data.Errors }); err != nil {
		return fmt.Errorf("delete record %s: %w", recordID, err)
	}
	if !data.Success {
		return fmt.Errorf("cloudflare: delete record %s failed", recordID)
	}
	return nil
}

// toCfRecord maps a desired record onto the API payload. The proxied flag is
// only meaningful for the record types Cloudflare can proxy; sending it for a
// TXT or MX record is rejected.
func toCfRecord(record entity.DnsRecord) cfRecord {
	body := cfRecord{
		ID:       record.ID,
		Type:     record.Type,
		Name:     record.Name,
		Content:  record.Value,
		TTL:      record.TTL,
		Priority: record.Priority,
		Comment:  record.Comment,
	}

	switch strings.ToUpper(record.Type) {
	case "A", "AAAA", "CNAME":
		proxied := record.Proxied
		body.Proxied = &proxied

	case "SRV", "TLSA":
		// These types are rejected with a flat content string; the API wants
		// the components broken out.
		if data := structuredData(record); data != nil {
			body.Data = data
			body.Content = ""
			body.Priority = nil
		}
	}

	return body
}

// structuredData splits an SRV or TLSA record value into the field map the
// Cloudflare API expects. It returns nil when the value is not in the
// canonical presentation form, letting the caller fall back to content.
func structuredData(record entity.DnsRecord) map[string]any {
	fields := strings.Fields(record.Value)

	switch strings.ToUpper(record.Type) {
	case "SRV":
		// Presentation form: "<priority> <weight> <port> <target>".
		if len(fields) != 4 {
			return nil
		}
		priority, err1 := strconv.Atoi(fields[0])
		weight, err2 := strconv.Atoi(fields[1])
		port, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return nil
		}
		return map[string]any{
			"priority": priority,
			"weight":   weight,
			"port":     port,
			"target":   strings.TrimSuffix(fields[3], "."),
		}

	case "TLSA":
		// Presentation form: "<usage> <selector> <matching type> <data>".
		if len(fields) != 4 {
			return nil
		}
		usage, err1 := strconv.Atoi(fields[0])
		selector, err2 := strconv.Atoi(fields[1])
		matchingType, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return nil
		}
		return map[string]any{
			"usage":         usage,
			"selector":      selector,
			"matching_type": matchingType,
			"certificate":   fields[3],
		}
	}

	return nil
}

// renderStructuredValue is the inverse of structuredData: it rebuilds the
// presentation form so the reconciler can compare a published record against
// the desired one.
func renderStructuredValue(recordType string, data map[string]any) string {
	number := func(key string) string {
		switch v := data[key].(type) {
		case float64: // JSON numbers decode as float64
			return strconv.Itoa(int(v))
		case string:
			return v
		default:
			return ""
		}
	}
	text := func(key string) string {
		if v, ok := data[key].(string); ok {
			return strings.TrimSuffix(v, ".")
		}
		return ""
	}

	switch strings.ToUpper(recordType) {
	case "SRV":
		return strings.Join([]string{number("priority"), number("weight"), number("port"), text("target")}, " ")
	case "TLSA":
		return strings.Join([]string{number("usage"), number("selector"), number("matching_type"), text("certificate")}, " ")
	default:
		return ""
	}
}

// doJSON performs one API call, retrying while Cloudflare reports 429 and
// honouring Retry-After (PLAN.md section 7.5). It fails on any non-2xx status
// rather than handing a decoder an error page.
func (a *CloudflareAdapter) doJSON(ctx context.Context, method, reqURL string, body any, out any, errsOf func() []cfError) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	backoff := time.Second

	for attempt := 1; ; attempt++ {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+a.apiToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < rateLimitMaxAttempts {
			wait := retryAfter(resp.Header.Get("Retry-After"), backoff)
			resp.Body.Close()
			backoff = min(backoff*2, rateLimitMaxBackoff)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read response body: %w", readErr)
		}

		// Decode first so an error envelope can be quoted back to the operator,
		// then judge the status. Cloudflare returns its envelope on 4xx too.
		decodeErr := json.Unmarshal(respBody, out)

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("cloudflare API returned HTTP %d: %s", resp.StatusCode, describeErrors(errsOf(), respBody))
		}
		if decodeErr != nil {
			return fmt.Errorf("decode json: %w", decodeErr)
		}
		return nil
	}
}

func describeErrors(errs []cfError, raw []byte) string {
	if len(errs) > 0 {
		messages := make([]string, 0, len(errs))
		for _, e := range errs {
			messages = append(messages, e.Message)
		}
		return strings.Join(messages, "; ")
	}
	const maxRaw = 200
	if len(raw) > maxRaw {
		raw = raw[:maxRaw]
	}
	return strings.TrimSpace(string(raw))
}

// retryAfter reads the header as delta-seconds (RFC 9110 section 10.2.3),
// falling back to the caller's exponential backoff when it is absent or
// unparseable.
func retryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds < 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

// Ensure port interface compliance
var _ port.DnsProvider = (*CloudflareAdapter)(nil)
