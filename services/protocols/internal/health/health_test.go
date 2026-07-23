package health

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler_Live_AlwaysReturns200(t *testing.T) {
	h := Handler(func() error { return errors.New("db down") })
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /health/live status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandler_Ready_Returns200WhenPingSucceeds(t *testing.T) {
	h := Handler(func() error { return nil })
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /health/ready status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandler_Ready_Returns503WhenPingFails(t *testing.T) {
	h := Handler(func() error { return errors.New("db down") })
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /health/ready status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
