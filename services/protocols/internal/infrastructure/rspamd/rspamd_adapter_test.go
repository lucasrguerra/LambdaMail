package rspamd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/valueobject"
)

func TestRspamdAdapter_Scan_MapsActions(t *testing.T) {
	tests := []struct {
		rspamdAction string
		wantVerdict  valueobject.ScanVerdict
	}{
		{"no action", valueobject.ScanVerdictClean},
		{"add header", valueobject.ScanVerdictSpamJunk},
		{"greylist", valueobject.ScanVerdictGreylist},
		{"reject", valueobject.ScanVerdictSpamReject},
	}

	for _, tt := range tests {
		t.Run(tt.rspamdAction, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/checkv2" {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				resp := RspamdResponse{
					Action:        tt.rspamdAction,
					Score:         5.0,
					RequiredScore: 15.0,
					Symbols: map[string]map[string]interface{}{
						"SPF_PASS": {"score": -1.0},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			adapter := NewRspamdAdapter(ts.URL)
			res, err := adapter.Scan(context.Background(), port.ScanInput{Payload: []byte("test email")})
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}

			if res.Verdict != tt.wantVerdict {
				t.Errorf("verdict = %s, want %s", res.Verdict, tt.wantVerdict)
			}
		})
	}
}
