package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lambdamail/protocols/internal/domain/entity"
)

func TestCloudflareAdapter_GetZoneID_List_Create_Update_Delete(t *testing.T) {
	mux := http.NewServeMux()

	// GET /zones?name=example.test
	mux.HandleFunc("/zones", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if name == "example.test" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": []map[string]interface{}{
					{"id": "zone_12345", "name": "example.test"},
				},
			})
		} else {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"errors":  []map[string]string{{"message": "Zone not found"}},
			})
		}
	})

	// GET /zones/zone_12345/dns_records & POST /zones/zone_12345/dns_records
	mux.HandleFunc("/zones/zone_12345/dns_records", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": []map[string]interface{}{
					{
						"id":      "rec_1",
						"type":    "A",
						"name":    "mail.example.test",
						"content": "192.0.2.1",
						"ttl":     1,
						"proxied": false,
					},
				},
			})
		} else if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"id": "rec_2",
				},
			})
		}
	})

	// PUT /zones/zone_12345/dns_records/rec_1
	mux.HandleFunc("/zones/zone_12345/dns_records/rec_1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  map[string]interface{}{"id": "rec_1"},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	adapter := NewCloudflareAdapter("test_token")
	adapter.baseURL = ts.URL

	ctx := context.Background()

	// 1. GetZoneID
	zoneID, err := adapter.GetZoneID(ctx, "example.test")
	if err != nil || zoneID != "zone_12345" {
		t.Fatalf("GetZoneID: err=%v, zoneID=%s", err, zoneID)
	}

	// 2. ListRecords
	records, err := adapter.ListRecords(ctx, zoneID)
	if err != nil || len(records) != 1 || records[0].Name != "mail.example.test" {
		t.Fatalf("ListRecords: err=%v, records=%+v", err, records)
	}

	// 3. CreateRecord
	err = adapter.CreateRecord(ctx, zoneID, entity.DnsRecord{
		Type:  "TXT",
		Name:  "example.test",
		Value: "v=spf1 mx -all",
		TTL:   1,
	})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	// 4. UpdateRecord
	err = adapter.UpdateRecord(ctx, zoneID, entity.DnsRecord{
		ID:    "rec_1",
		Type:  "A",
		Name:  "mail.example.test",
		Value: "192.0.2.2",
		TTL:   1,
	})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
}
