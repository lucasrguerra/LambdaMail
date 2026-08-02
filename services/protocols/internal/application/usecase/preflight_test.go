package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// healthyDeps is a deployment where every probe succeeds; individual tests
// break exactly one thing so the failure being asserted is unambiguous.
func healthyDeps() PreflightDeps {
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	return PreflightDeps{
		DialPort25:        func(context.Context, string) error { return nil },
		LookupAddr:        func(_ context.Context, _ string) ([]string, error) { return []string{"mail.example.test."}, nil },
		LookupHost:        func(_ context.Context, _ string) ([]string, error) { return []string{"192.0.2.10"}, nil },
		LookupTXT:         func(_ context.Context, _ string) ([]string, error) { return nil, errors.New("NXDOMAIN") },
		HasCertificateFor: func(string) bool { return true },
		FetchMtaSts:       func(context.Context, string) error { return nil },
		FreeDiskPercent:   func() (float64, error) { return 65, nil },
		Now:               func() time.Time { return now },
		NetworkTime:       func(context.Context) (time.Time, error) { return now, nil },
	}
}

func healthyInput() PreflightInput {
	return PreflightInput{
		PrimaryMailHost: "mail.example.test",
		MailDomain:      "example.test",
		PublicIPv4:      "192.0.2.10",
		TLSMode:         "traefik",
	}
}

func findCheck(t *testing.T, report PreflightReport, substring string) CheckResult {
	t.Helper()
	for _, result := range report.Results {
		if strings.Contains(result.Name, substring) {
			return result
		}
	}
	t.Fatalf("no check matching %q in report:\n%s", substring, report)
	return CheckResult{}
}

func TestPreflight_HealthyDeploymentDoesNotBlock(t *testing.T) {
	report := NewPreflightUseCase(healthyDeps()).Execute(context.Background(), healthyInput())

	if report.Blocking() {
		t.Errorf("a healthy deployment was reported as blocking:\n%s", report)
	}
}

// PLAN.md premise 1: without outbound port 25 there is no direct delivery.
func TestPreflight_BlockedPort25IsCriticalAndSuggestsSmarthost(t *testing.T) {
	deps := healthyDeps()
	deps.DialPort25 = func(context.Context, string) error { return errors.New("connection timed out") }

	report := NewPreflightUseCase(deps).Execute(context.Background(), healthyInput())
	check := findCheck(t, report, "port 25")

	if check.Status != CheckFailed || check.Severity != SeverityCritical {
		t.Errorf("check = %+v, want a critical failure", check)
	}
	if !strings.Contains(check.Remedy, "smarthost") {
		t.Errorf("remedy should point at the smarthost fallback: %q", check.Remedy)
	}
	if !report.Blocking() {
		t.Error("a blocked port 25 must block")
	}
}

// With a smarthost configured, direct port 25 delivery is not used, so the
// check must not block the deployment.
func TestPreflight_SmarthostSkipsPort25Check(t *testing.T) {
	deps := healthyDeps()
	deps.DialPort25 = func(context.Context, string) error { return errors.New("blocked") }

	input := healthyInput()
	input.SmarthostSet = true

	report := NewPreflightUseCase(deps).Execute(context.Background(), input)

	if findCheck(t, report, "port 25").Status != CheckSkipped {
		t.Error("the port 25 check should be skipped when a smarthost is configured")
	}
	if report.Blocking() {
		t.Errorf("a smarthost deployment was blocked:\n%s", report)
	}
}

// PLAN.md premise 2: PTR delegation belongs to the hosting provider, so the
// remedy has to say so.
func TestPreflight_MissingPtrIsCriticalAndNamesTheProvider(t *testing.T) {
	deps := healthyDeps()
	deps.LookupAddr = func(context.Context, string) ([]string, error) { return nil, errors.New("NXDOMAIN") }

	report := NewPreflightUseCase(deps).Execute(context.Background(), healthyInput())
	check := findCheck(t, report, "PTR")

	if check.Status != CheckFailed || check.Severity != SeverityCritical {
		t.Errorf("check = %+v, want a critical failure", check)
	}
	if !strings.Contains(check.Remedy, "hosting provider") {
		t.Errorf("remedy should name the hosting provider: %q", check.Remedy)
	}
}

func TestPreflight_ForwardConfirmedReverseDnsMismatchIsCritical(t *testing.T) {
	deps := healthyDeps()
	deps.LookupHost = func(context.Context, string) ([]string, error) { return []string{"198.51.100.1"}, nil }

	report := NewPreflightUseCase(deps).Execute(context.Background(), healthyInput())

	if check := findCheck(t, report, "FCrDNS"); check.Status != CheckFailed {
		t.Errorf("check = %+v, want a failure when the A record and the PTR disagree", check)
	}
}

func TestPreflight_ListedIpIsCriticalAndNamesTheList(t *testing.T) {
	deps := healthyDeps()
	deps.LookupTXT = func(_ context.Context, name string) ([]string, error) {
		if strings.Contains(name, "zen.spamhaus.org") {
			return []string{"https://www.spamhaus.org/query/ip/192.0.2.10"}, nil
		}
		return nil, errors.New("NXDOMAIN")
	}

	report := NewPreflightUseCase(deps).Execute(context.Background(), healthyInput())
	check := findCheck(t, report, "blocklist")

	if check.Status != CheckFailed || check.Severity != SeverityCritical {
		t.Errorf("check = %+v, want a critical failure", check)
	}
	if !strings.Contains(check.Detail, "Spamhaus") {
		t.Errorf("detail should name the list: %q", check.Detail)
	}
}

// PLAN.md section 8.2 trap 2: Traefik only holds certificates for hosts it
// routes, so the remedy has to explain the missing router.
func TestPreflight_MissingCertificateExplainsTheTraefikRouter(t *testing.T) {
	deps := healthyDeps()
	deps.HasCertificateFor = func(string) bool { return false }

	report := NewPreflightUseCase(deps).Execute(context.Background(), healthyInput())
	check := findCheck(t, report, "certificate is loaded")

	if check.Status != CheckFailed || check.Severity != SeverityCritical {
		t.Errorf("check = %+v, want a critical failure", check)
	}
	if !strings.Contains(check.Remedy, "router") {
		t.Errorf("remedy should explain the missing router: %q", check.Remedy)
	}
}

// PLAN.md section 5.1: enabling DANE under Traefik-managed certificates
// guarantees breakage at the next renewal, so the preflight must refuse it.
func TestPreflight_DaneUnderTraefikModeIsRefused(t *testing.T) {
	input := healthyInput()
	input.DaneEnabled = true
	input.TLSMode = "traefik"

	report := NewPreflightUseCase(healthyDeps()).Execute(context.Background(), input)
	check := findCheck(t, report, "DANE")

	if check.Status != CheckFailed || check.Severity != SeverityCritical {
		t.Errorf("check = %+v, want a critical failure", check)
	}
	if !report.Blocking() {
		t.Error("DANE under Traefik mode must block the deployment")
	}
}

func TestPreflight_DaneUnderAcmeModePasses(t *testing.T) {
	input := healthyInput()
	input.DaneEnabled = true
	input.TLSMode = "acme"

	report := NewPreflightUseCase(healthyDeps()).Execute(context.Background(), input)

	if check := findCheck(t, report, "DANE"); check.Status != CheckPassed {
		t.Errorf("check = %+v, want a pass under TLS_MODE=acme", check)
	}
}

// DKIM signatures and TOTP codes both break on a drifting clock, but neither
// loses mail outright, so this is high rather than critical.
func TestPreflight_ClockDriftIsHighNotCritical(t *testing.T) {
	deps := healthyDeps()
	local := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	deps.Now = func() time.Time { return local.Add(90 * time.Second) }
	deps.NetworkTime = func(context.Context) (time.Time, error) { return local, nil }

	report := NewPreflightUseCase(deps).Execute(context.Background(), healthyInput())
	check := findCheck(t, report, "clock")

	if check.Status != CheckFailed || check.Severity != SeverityHigh {
		t.Errorf("check = %+v, want a high-severity failure", check)
	}
	if report.Blocking() {
		t.Error("clock drift alone must not block the deployment")
	}
}

func TestPreflight_LowDiskIsMediumSeverity(t *testing.T) {
	deps := healthyDeps()
	deps.FreeDiskPercent = func() (float64, error) { return 4, nil }

	report := NewPreflightUseCase(deps).Execute(context.Background(), healthyInput())
	check := findCheck(t, report, "free space")

	if check.Status != CheckFailed || check.Severity != SeverityMedium {
		t.Errorf("check = %+v, want a medium-severity failure", check)
	}
}

// PLAN.md premise 5: half-configured IPv6 is a common cause of rejection, and
// disabling it is a valid answer.
func TestPreflight_IPv6WithoutPtrFailsButDisabledIPv6Passes(t *testing.T) {
	deps := healthyDeps()
	deps.LookupAddr = func(_ context.Context, ip string) ([]string, error) {
		if strings.Contains(ip, ":") {
			return nil, errors.New("NXDOMAIN")
		}
		return []string{"mail.example.test."}, nil
	}

	withIPv6 := healthyInput()
	withIPv6.PublicIPv6 = "2001:db8::1"

	report := NewPreflightUseCase(deps).Execute(context.Background(), withIPv6)
	if check := findCheck(t, report, "IPv6"); check.Status != CheckFailed {
		t.Errorf("check = %+v, want a failure for IPv6 without a PTR", check)
	}

	report = NewPreflightUseCase(deps).Execute(context.Background(), healthyInput())
	if check := findCheck(t, report, "IPv6"); check.Status != CheckPassed {
		t.Errorf("check = %+v, want a pass when IPv6 is disabled", check)
	}
}

// A probe that cannot run is reported as skipped, never silently as a pass.
func TestPreflight_MissingProbesAreSkippedNotPassed(t *testing.T) {
	report := NewPreflightUseCase(PreflightDeps{}).Execute(context.Background(), healthyInput())

	for _, result := range report.Results {
		if result.Status == CheckPassed && result.Name != "IPv6 is either fully configured or disabled" {
			t.Errorf("check %q passed with no probe configured", result.Name)
		}
	}
	if report.Blocking() {
		t.Error("skipped checks must not block")
	}
}

func TestReverseIPv4(t *testing.T) {
	if got := reverseIPv4("192.0.2.10"); got != "10.2.0.192" {
		t.Errorf("reverseIPv4 = %q, want 10.2.0.192", got)
	}
	if got := reverseIPv4("2001:db8::1"); got != "" {
		t.Errorf("reverseIPv4 of an IPv6 address = %q, want empty", got)
	}
}
