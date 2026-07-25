package rspamd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/valueobject"
)

type RspamdResponse struct {
	Action        string                         `json:"action"`
	Score         float64                        `json:"score"`
	RequiredScore float64                        `json:"required_score"`
	Symbols       map[string]map[string]interface{} `json:"symbols"`
}

type RspamdAdapter struct {
	baseURL    string
	httpClient *http.Client
}

func NewRspamdAdapter(baseURL string) *RspamdAdapter {
	if baseURL == "" {
		baseURL = "http://localhost:11333"
	}
	return &RspamdAdapter{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{},
	}
}

func (a *RspamdAdapter) Scan(ctx context.Context, input port.ScanInput) (*valueobject.ScanResult, error) {
	url := fmt.Sprintf("%s/checkv2", a.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(input.Payload))
	if err != nil {
		return nil, fmt.Errorf("create rspamd request: %w", err)
	}

	if input.ClientIP != "" {
		req.Header.Set("IP", input.ClientIP)
	}
	if input.HeloDomain != "" {
		req.Header.Set("Helo", input.HeloDomain)
	}
	if input.Sender != "" {
		req.Header.Set("From", input.Sender)
	}
	if input.Recipient != "" {
		req.Header.Set("Deliver-To", input.Recipient)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rspamd http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rspamd returned HTTP %d", resp.StatusCode)
	}

	var parsed RspamdResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode rspamd json: %w", err)
	}

	res := &valueobject.ScanResult{
		Score:         parsed.Score,
		RequiredScore: parsed.RequiredScore,
		Action:        parsed.Action,
		Symbols:       make(map[string]float64),
		HeadersToAdd:  make(map[string]string),
	}

	for name, obj := range parsed.Symbols {
		if scoreVal, ok := obj["score"].(float64); ok {
			res.Symbols[name] = scoreVal
		}
	}

	switch parsed.Action {
	case "reject":
		res.Verdict = valueobject.ScanVerdictSpamReject
	case "greylist":
		res.Verdict = valueobject.ScanVerdictGreylist
	case "add header", "rewrite subject":
		res.Verdict = valueobject.ScanVerdictSpamJunk
		res.HeadersToAdd["X-Spam-Flag"] = "YES"
		res.HeadersToAdd["X-Spam-Score"] = fmt.Sprintf("%.2f", parsed.Score)
	default:
		res.Verdict = valueobject.ScanVerdictClean
		res.HeadersToAdd["X-Spam-Flag"] = "NO"
		res.HeadersToAdd["X-Spam-Score"] = fmt.Sprintf("%.2f", parsed.Score)
	}

	return res, nil
}
