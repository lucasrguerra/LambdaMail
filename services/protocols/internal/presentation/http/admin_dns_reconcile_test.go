package httppresentation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lambdamail/protocols/internal/domain/entity"
)

// Making the reconcile button do what its name says.
//
// It answered {"status":"REQUESTED","message":"DNS records are not verified by
// this endpoint yet"} - it wrote an audit row and nothing else. The service
// that can actually create a record, with the provider token in hand, exposed
// no route at all, so the console could report a record missing and had no way
// to fix it.

type stubReconciler struct {
	called  []string
	created int
	err     error
}

func (s *stubReconciler) ReconcileDomain(_ context.Context, domain string) (ReconcileResult, error) {
	s.called = append(s.called, domain)
	if s.err != nil {
		return ReconcileResult{}, s.err
	}
	return ReconcileResult{Created: s.created, Domain: domain}, nil
}

func newReconcileAPI(t *testing.T, r *stubReconciler) *adminDnsAPI {
	t.Helper()
	return &adminDnsAPI{sessions: testVerifier(interopSecret), reconciler: r}
}

func adminRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+adminSurfaceToken2())
	return req
}

func TestReconcileActuallyReconcilesTheDomain(t *testing.T) {
	stub := &stubReconciler{created: 11}
	rec := httptest.NewRecorder()
	newReconcileAPI(t, stub).handleReconcile(rec, adminRequest("/api/v1/admin/dns/reconcile?domain=example.test"))

	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body)
	}
	if len(stub.called) != 1 || stub.called[0] != "example.test" {
		t.Errorf("reconciled %v, want [example.test]", stub.called)
	}

	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	// It reports what it did. The old endpoint reported a status it had not
	// established, which is how the console came to claim records were
	// verified when nothing had looked at them.
	if body["created"] != float64(11) {
		t.Errorf("reported %v created", body["created"])
	}
}

func TestReconcileRefusesWithoutAnAdminSession(t *testing.T) {
	stub := &stubReconciler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/dns/reconcile?domain=example.test", nil)
	newReconcileAPI(t, stub).handleReconcile(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("answered %d without a session", rec.Code)
	}
	if len(stub.called) != 0 {
		t.Error("it wrote to DNS without an authenticated caller")
	}
}

// A user session must not reach it: creating DNS records is an admin action.
func TestReconcileRefusesAUserSession(t *testing.T) {
	stub := &stubReconciler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/dns/reconcile?domain=example.test", nil)
	req.Header.Set("Authorization", "Bearer "+interopSession)
	newReconcileAPI(t, stub).handleReconcile(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a webmail session reached the reconciler: %d", rec.Code)
	}
	if len(stub.called) != 0 {
		t.Error("a webmail session wrote to DNS")
	}
}

func TestReconcileRequiresADomain(t *testing.T) {
	stub := &stubReconciler{}
	rec := httptest.NewRecorder()
	newReconcileAPI(t, stub).handleReconcile(rec, adminRequest("/api/v1/admin/dns/reconcile"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("answered %d with no domain", rec.Code)
	}
	if len(stub.called) != 0 {
		t.Error("it reconciled something with no domain named")
	}
}

// A provider that refuses has to be reported as a failure, not swallowed into
// a cheerful answer the way the old endpoint did.
func TestReconcileReportsAProviderFailure(t *testing.T) {
	stub := &stubReconciler{err: errors.New("cloudflare refused the token")}
	rec := httptest.NewRecorder()
	newReconcileAPI(t, stub).handleReconcile(rec, adminRequest("/api/v1/admin/dns/reconcile?domain=example.test"))

	if rec.Code < 500 {
		t.Errorf("a provider failure answered %d, want a 5xx", rec.Code)
	}
	// The provider's own words must not travel to the browser: they can carry
	// account identifiers.
	if strings.Contains(rec.Body.String(), "cloudflare refused the token") {
		t.Errorf("the provider error leaked: %s", rec.Body)
	}
}

// Without the reconciler wired, the route says so rather than pretending.
func TestReconcileSaysWhenItIsNotConfigured(t *testing.T) {
	rec := httptest.NewRecorder()
	api := &adminDnsAPI{sessions: testVerifier(interopSecret)}
	api.handleReconcile(rec, adminRequest("/api/v1/admin/dns/reconcile?domain=example.test"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("answered %d with no reconciler configured", rec.Code)
	}
}

func adminSurfaceToken2() string {
	return mintSessionRaw(WebSession{
		Subject: "mb-1", Email: "admin@example.test", Role: "SUPER_ADMIN",
		Surface: "admin", Audience: "lambdamail:admin",
		Purpose: "session", MfaSatisfied: true,
		IssuedAt: interopIssuedAt, ExpiresAt: interopIssuedAt + 3600,
	})
}

// stubSpec is the minimum a wired DNS API needs; the wiring, not the spec, is
// what these tests are about.
type stubSpec struct{}

func (stubSpec) ExpectedRecords(context.Context, string) ([]entity.DnsRecord, error) {
	return nil, nil
}

type stubVerifier struct{}

func (stubVerifier) VerifyRecord(context.Context, entity.DnsRecord) (bool, string) {
	return true, ""
}

// The console reported "DNS reconciliation needs a provider token to be
// configured" while the token was configured and working - the background
// sweep was publishing records with it at that very moment.
//
// SetDnsReconciler only assigned when r.dns already existed, and main.go calls
// it before SetAdminDnsAPI creates r.dns. So it silently did nothing, and the
// API was then built with no reconciler at all. The earlier tests missed it by
// constructing adminDnsAPI directly, which is exactly the wiring that broke.
func TestReconcilerSurvivesBeingWiredBeforeTheDnsAPI(t *testing.T) {
	for _, order := range []string{"reconciler first", "dns api first"} {
		t.Run(order, func(t *testing.T) {
			router := NewRouter(nil, func() error { return nil })
			stub := &stubReconciler{created: 11}

			wireAPI := func() { router.SetAdminDnsAPI(stubSpec{}, stubVerifier{}, interopSecret) }
			wireReconciler := func() { router.SetDnsReconciler(stub) }

			if order == "reconciler first" {
				wireReconciler()
				wireAPI()
			} else {
				wireAPI()
				wireReconciler()
			}

			// Only the clock is pinned: the captured admin token has a fixed
			// validity window. The wiring itself is left exactly as main.go
			// builds it, because the wiring is what is under test.
			router.dns.sessions.now = func() time.Time { return time.Unix(interopIssuedAt+60, 0) }

			rec := httptest.NewRecorder()
			router.handleAdminDnsReconcile(rec, adminRequest("/api/v1/admin/dns/reconcile?domain=example.test"))

			if rec.Code != http.StatusOK {
				t.Fatalf("answered %d (%s): the route lost its reconciler", rec.Code, rec.Body)
			}
			if len(stub.called) != 1 {
				t.Fatalf("reconciled %v, want one call: the route answered without reconciling", stub.called)
			}
		})
	}
}
