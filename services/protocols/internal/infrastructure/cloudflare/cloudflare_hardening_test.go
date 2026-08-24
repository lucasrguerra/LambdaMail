package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lambdamail/protocols/internal/domain/entity"
)

// A proxied A record for the mail host makes Cloudflare answer with its own
// anycast addresses, which do not speak SMTP - the domain stops receiving mail.
// PLAN.md section 7.1 requires proxied=false, so the flag must be sent
// explicitly rather than left to the zone default.
func TestCloudflareAdapter_CreateRecord_SendsProxiedFalseExplicitly(t *testing.T) {
	var body map[string]interface{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": map[string]interface{}{"id": "rec_1"}})
	}))
	defer ts.Close()

	adapter := NewCloudflareAdapter("token")
	adapter.baseURL = ts.URL

	err := adapter.CreateRecord(context.Background(), "zone1", entity.DnsRecord{
		Type: "A", Name: "mail.example.test", Value: "192.0.2.1", TTL: 1, Proxied: false,
	})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	proxied, present := body["proxied"]
	if !present {
		t.Fatal("proxied flag was omitted from the request; Cloudflare would fall back to the zone default")
	}
	if proxied != false {
		t.Errorf("proxied = %v, want false", proxied)
	}
}

// An HTTP-level failure carries no Cloudflare envelope, so a decoder-only
// check reports a confusing JSON error - or worse, silently succeeds.
func TestCloudflareAdapter_ReportsHttpErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"success":false,"errors":[{"message":"Insufficient permissions"}]}`))
	}))
	defer ts.Close()

	adapter := NewCloudflareAdapter("token")
	adapter.baseURL = ts.URL

	_, err := adapter.GetZoneID(context.Background(), "example.test")
	if err == nil {
		t.Fatal("expected an error for HTTP 403")
	}
	if !strings.Contains(err.Error(), "Insufficient permissions") {
		t.Errorf("error should surface the API message, got: %v", err)
	}
}

// PLAN.md section 7.5 lists the record listing as paginated. Stopping at the
// first page makes the reconciler believe records are missing and try to
// re-create them, which the API then rejects.
func TestCloudflareAdapter_ListRecords_FollowsPagination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": []map[string]interface{}{
					{"id": "rec_1", "type": "A", "name": "a.example.test", "content": "192.0.2.1"},
				},
				"result_info": map[string]interface{}{"page": 1, "total_pages": 2},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": []map[string]interface{}{
				{"id": "rec_2", "type": "A", "name": "b.example.test", "content": "192.0.2.2"},
			},
			"result_info": map[string]interface{}{"page": 2, "total_pages": 2},
		})
	}))
	defer ts.Close()

	adapter := NewCloudflareAdapter("token")
	adapter.baseURL = ts.URL

	records, err := adapter.ListRecords(context.Background(), "zone1")
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 across both pages", len(records))
	}
}

// PLAN.md section 7.5 requires retrying 429 while honouring Retry-After.
func TestCloudflareAdapter_RetriesOnRateLimit(t *testing.T) {
	var calls int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  []map[string]interface{}{{"id": "zone_1", "name": "example.test"}},
		})
	}))
	defer ts.Close()

	adapter := NewCloudflareAdapter("token")
	adapter.baseURL = ts.URL

	zoneID, err := adapter.GetZoneID(context.Background(), "example.test")
	if err != nil {
		t.Fatalf("GetZoneID: %v", err)
	}
	if zoneID != "zone_1" {
		t.Errorf("zoneID = %q, want zone_1", zoneID)
	}
	if calls != 2 {
		t.Errorf("made %d calls, want 2 (one rate limited, one retried)", calls)
	}
}

// A failed delete that reports success would silently leave stale records in
// the zone.
func TestCloudflareAdapter_DeleteRecord_ReportsFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"success":false,"errors":[{"message":"Record not found"}]}`))
	}))
	defer ts.Close()

	adapter := NewCloudflareAdapter("token")
	adapter.baseURL = ts.URL

	if err := adapter.DeleteRecord(context.Background(), "zone1", "rec_missing"); err == nil {
		t.Fatal("expected an error when the delete fails")
	}
}

// Cloudflare rejects SRV and TLSA records submitted as a flat content string;
// they must be broken into the structured data object.
func TestCloudflareAdapter_SendsStructuredDataForSrvAndTlsa(t *testing.T) {
	cases := []struct {
		name     string
		record   entity.DnsRecord
		expected map[string]interface{}
	}{
		{
			name:   "SRV",
			record: entity.DnsRecord{Type: "SRV", Name: "_imaps._tcp.example.test", Value: "0 1 993 mail.example.test", TTL: 1},
			expected: map[string]interface{}{
				"priority": float64(0), "weight": float64(1), "port": float64(993), "target": "mail.example.test",
			},
		},
		{
			name:   "TLSA",
			record: entity.DnsRecord{Type: "TLSA", Name: "_25._tcp.mail.example.test", Value: "3 1 1 " + strings.Repeat("ab", 32), TTL: 1},
			expected: map[string]interface{}{
				"usage": float64(3), "selector": float64(1), "matching_type": float64(1), "certificate": strings.Repeat("ab", 32),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]interface{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&body)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": map[string]interface{}{"id": "rec_1"}})
			}))
			defer ts.Close()

			adapter := NewCloudflareAdapter("token")
			adapter.baseURL = ts.URL

			if err := adapter.CreateRecord(context.Background(), "zone1", tc.record); err != nil {
				t.Fatalf("CreateRecord: %v", err)
			}

			data, ok := body["data"].(map[string]interface{})
			if !ok {
				t.Fatalf("no structured data object sent; body = %v", body)
			}
			for key, want := range tc.expected {
				if data[key] != want {
					t.Errorf("data[%q] = %v, want %v", key, data[key], want)
				}
			}
			if content, present := body["content"]; present && content != "" {
				t.Errorf("content was sent alongside data: %v", content)
			}
		})
	}
}

// A record returned with only a data object must round-trip back to the
// presentation form, or the reconciler sees an empty value and rewrites it on
// every run.
func TestCloudflareAdapter_ListRecords_RendersStructuredValues(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": []map[string]interface{}{{
				"id": "rec_1", "type": "SRV", "name": "_imaps._tcp.example.test",
				"data": map[string]interface{}{"priority": 0, "weight": 1, "port": 993, "target": "mail.example.test"},
			}},
			"result_info": map[string]interface{}{"page": 1, "total_pages": 1},
		})
	}))
	defer ts.Close()

	adapter := NewCloudflareAdapter("token")
	adapter.baseURL = ts.URL

	records, err := adapter.ListRecords(context.Background(), "zone1")
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 1 || records[0].Value != "0 1 993 mail.example.test" {
		t.Errorf("value = %q, want the rendered presentation form", records[0].Value)
	}
}

// Cloudflare renders an SRV record's content WITHOUT the priority: the zone
// holding "0 1 993 mail.example.test" answers content "1 993 mail.example.test"
// and keeps the priority in data. Taking content at face value made the record
// differ from the spec on every comparison, so every reconcile rewrote all four
// SRV records - against the live zone, on a timer, forever.
func TestCloudflareAdapter_ListRecords_KeepsSrvPriority(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": []map[string]interface{}{{
				"id": "rec_1", "type": "SRV", "name": "_imaps._tcp.example.test",
				// Exactly what the API returns: the priority is missing here
				// and present in data.
				"content": "1 993 mail.example.test",
				"data": map[string]interface{}{
					"priority": float64(0), "weight": float64(1),
					"port": float64(993), "target": "mail.example.test",
				},
				"ttl": float64(1),
			}},
			"result_info": map[string]interface{}{"total_pages": float64(1)},
		})
	}))
	defer ts.Close()

	adapter := NewCloudflareAdapter("token")
	adapter.baseURL = ts.URL

	records, err := adapter.ListRecords(context.Background(), "zone1")
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	const want = "0 1 993 mail.example.test"
	if records[0].Value != want {
		t.Errorf("read SRV value %q, want %q: the record will be rewritten on every reconcile",
			records[0].Value, want)
	}
}
