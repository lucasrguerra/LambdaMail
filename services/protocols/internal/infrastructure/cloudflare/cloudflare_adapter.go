package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

type CloudflareAdapter struct {
	apiToken string
	baseURL  string
	httpClient *http.Client
}

func NewCloudflareAdapter(apiToken string) *CloudflareAdapter {
	return &CloudflareAdapter{
		apiToken:   apiToken,
		baseURL:    "https://api.cloudflare.com/client/v4",
		httpClient: &http.Client{},
	}
}

type cfZoneResponse struct {
	Success bool `json:"success"`
	Result  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"result"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type cfRecord struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"`
	Proxied  bool   `json:"proxied,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

type cfRecordResponse struct {
	Success bool       `json:"success"`
	Result  []cfRecord `json:"result"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type cfSingleRecordResponse struct {
	Success bool     `json:"success"`
	Result  cfRecord `json:"result"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (a *CloudflareAdapter) GetZoneID(ctx context.Context, domainName string) (string, error) {
	reqURL := fmt.Sprintf("%s/zones?name=%s", a.baseURL, url.QueryEscape(domainName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	a.setHeaders(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var data cfZoneResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("decode json: %w", err)
	}

	if !data.Success || len(data.Result) == 0 {
		errMsg := "zone not found"
		if len(data.Errors) > 0 {
			errMsg = data.Errors[0].Message
		}
		return "", fmt.Errorf("cloudflare API error: %s", errMsg)
	}

	return data.Result[0].ID, nil
}

func (a *CloudflareAdapter) ListRecords(ctx context.Context, zoneID string) ([]entity.DnsRecord, error) {
	reqURL := fmt.Sprintf("%s/zones/%s/dns_records?per_page=100", a.baseURL, zoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	a.setHeaders(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data cfRecordResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	if !data.Success {
		return nil, fmt.Errorf("cloudflare API error listing records")
	}

	var out []entity.DnsRecord
	for _, r := range data.Result {
		out = append(out, entity.DnsRecord{
			ID:       r.ID,
			Type:     r.Type,
			Name:     r.Name,
			Value:    r.Content,
			TTL:      r.TTL,
			Priority: r.Priority,
			Proxied:  r.Proxied,
			Comment:  r.Comment,
		})
	}
	return out, nil
}

func (a *CloudflareAdapter) CreateRecord(ctx context.Context, zoneID string, record entity.DnsRecord) error {
	reqURL := fmt.Sprintf("%s/zones/%s/dns_records", a.baseURL, zoneID)
	cfBody := cfRecord{
		Type:     record.Type,
		Name:     record.Name,
		Content:  record.Value,
		TTL:      record.TTL,
		Priority: record.Priority,
		Proxied:  record.Proxied,
		Comment:  record.Comment,
	}

	payload, err := json.Marshal(cfBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	a.setHeaders(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var data cfSingleRecordResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	if !data.Success {
		errMsg := "create record failed"
		if len(data.Errors) > 0 {
			errMsg = data.Errors[0].Message
		}
		return fmt.Errorf("cloudflare API error: %s", errMsg)
	}

	return nil
}

func (a *CloudflareAdapter) UpdateRecord(ctx context.Context, zoneID string, record entity.DnsRecord) error {
	reqURL := fmt.Sprintf("%s/zones/%s/dns_records/%s", a.baseURL, zoneID, record.ID)
	cfBody := cfRecord{
		Type:     record.Type,
		Name:     record.Name,
		Content:  record.Value,
		TTL:      record.TTL,
		Priority: record.Priority,
		Proxied:  record.Proxied,
		Comment:  record.Comment,
	}

	payload, err := json.Marshal(cfBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	a.setHeaders(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var data cfSingleRecordResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	if !data.Success {
		return fmt.Errorf("cloudflare API error updating record")
	}

	return nil
}

func (a *CloudflareAdapter) DeleteRecord(ctx context.Context, zoneID string, recordID string) error {
	reqURL := fmt.Sprintf("%s/zones/%s/dns_records/%s", a.baseURL, zoneID, recordID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return err
	}
	a.setHeaders(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (a *CloudflareAdapter) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+a.apiToken)
	req.Header.Set("Content-Type", "application/json")
}

// Ensure port interface compliance
var _ port.DnsProvider = (*CloudflareAdapter)(nil)
