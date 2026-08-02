package usecase

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Severity mirrors the table in PLAN.md section 15.
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
)

// CheckStatus is the outcome of one preflight check.
type CheckStatus string

const (
	CheckPassed  CheckStatus = "PASS"
	CheckFailed  CheckStatus = "FAIL"
	CheckSkipped CheckStatus = "SKIP"
)

// CheckResult is one line of the preflight report.
type CheckResult struct {
	Name     string
	Status   CheckStatus
	Severity Severity
	Detail   string
	// Remedy is the operator action that fixes it. Several of these checks
	// cover steps PLAN.md section 7.4 lists as not automatable, so telling
	// the operator exactly what to do is the point of the check.
	Remedy string
}

// PreflightReport aggregates the checks.
type PreflightReport struct {
	Results []CheckResult
}

// Blocking reports whether any critical check failed. PLAN.md section 15 uses
// this to keep a domain out of the VERIFIED state.
func (r PreflightReport) Blocking() bool {
	for _, result := range r.Results {
		if result.Status == CheckFailed && result.Severity == SeverityCritical {
			return true
		}
	}
	return false
}

func (r PreflightReport) String() string {
	var sb strings.Builder
	for _, result := range r.Results {
		fmt.Fprintf(&sb, "[%-4s] %-10s %s", result.Status, result.Severity, result.Name)
		if result.Detail != "" {
			fmt.Fprintf(&sb, "\n           %s", result.Detail)
		}
		if result.Status == CheckFailed && result.Remedy != "" {
			fmt.Fprintf(&sb, "\n           remedy: %s", result.Remedy)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// PreflightDeps are the environment probes the checks need. They are injected
// so the whole preflight is testable without touching the network.
type PreflightDeps struct {
	// DialPort25 reports whether outbound port 25 reaches a known MX.
	DialPort25 func(ctx context.Context, address string) error
	// LookupAddr resolves the PTR of an IP.
	LookupAddr func(ctx context.Context, ip string) ([]string, error)
	// LookupHost resolves a name to addresses.
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// LookupTXT resolves TXT records, used for the DNSBL checks.
	LookupTXT func(ctx context.Context, name string) ([]string, error)
	// HasCertificateFor reports whether a certificate is loaded for a host.
	HasCertificateFor func(host string) bool
	// FetchMtaSts fetches the served MTA-STS policy over HTTPS.
	FetchMtaSts func(ctx context.Context, domain string) error
	// FreeDiskPercent reports the free space on the mail spool.
	FreeDiskPercent func() (float64, error)
	// Now is the local clock; drift against NTP breaks DKIM and TOTP.
	Now func() time.Time
	// NetworkTime returns a trusted time to compare the local clock against.
	NetworkTime func(ctx context.Context) (time.Time, error)
}

// PreflightInput describes the deployment being checked.
type PreflightInput struct {
	PrimaryMailHost string
	MailDomain      string
	PublicIPv4      string
	PublicIPv6      string
	DaneEnabled     bool
	TLSMode         string
	SmarthostSet    bool
}

// PreflightUseCase implements PLAN.md section 15.
type PreflightUseCase struct {
	deps PreflightDeps
}

func NewPreflightUseCase(deps PreflightDeps) *PreflightUseCase {
	return &PreflightUseCase{deps: deps}
}

// wellKnownMX is the probe target of PLAN.md section 15. Google is used
// because it is the destination most likely to matter and least likely to be
// down.
const wellKnownMX = "gmail-smtp-in.l.google.com:25"

// dnsblZones are the reputation lists PLAN.md section 5.2 names.
var dnsblZones = map[string]string{
	"zen.spamhaus.org":       "Spamhaus ZEN",
	"b.barracudacentral.org": "Barracuda",
	"dnsbl.sorbs.net":        "SORBS",
}

func (uc *PreflightUseCase) Execute(ctx context.Context, input PreflightInput) PreflightReport {
	report := PreflightReport{}
	add := func(r CheckResult) { report.Results = append(report.Results, r) }

	add(uc.checkPort25(ctx, input))
	add(uc.checkReverseDNS(ctx, input))
	add(uc.checkForwardConfirmedReverseDNS(ctx, input))
	add(uc.checkBlocklists(ctx, input))
	add(uc.checkCertificate(input))
	add(uc.checkDaneRequiresAcmeMode(input))
	add(uc.checkMtaStsEndpoint(ctx, input))
	add(uc.checkClockDrift(ctx))
	add(uc.checkDiskSpace())
	add(uc.checkIPv6Coherence(ctx, input))

	return report
}

func (uc *PreflightUseCase) checkPort25(ctx context.Context, input PreflightInput) CheckResult {
	const name = "Outbound port 25 reaches a public MX"

	if input.SmarthostSet {
		return CheckResult{
			Name: name, Status: CheckSkipped, Severity: SeverityCritical,
			Detail: "a smarthost is configured, so direct delivery on port 25 is not used",
		}
	}
	if uc.deps.DialPort25 == nil {
		return skipped(name, SeverityCritical)
	}

	if err := uc.deps.DialPort25(ctx, wellKnownMX); err != nil {
		return CheckResult{
			Name: name, Status: CheckFailed, Severity: SeverityCritical,
			Detail: fmt.Sprintf("could not reach %s: %v", wellKnownMX, err),
			Remedy: "most VPS providers block egress on port 25 by default; ask the provider to unblock it, or configure RELAY_HOST to deliver through a smarthost",
		}
	}
	return passed(name, SeverityCritical, "")
}

func (uc *PreflightUseCase) checkReverseDNS(ctx context.Context, input PreflightInput) CheckResult {
	const name = "PTR of the public IP resolves to the mail host"

	if uc.deps.LookupAddr == nil || input.PublicIPv4 == "" {
		return skipped(name, SeverityCritical)
	}

	names, err := uc.deps.LookupAddr(ctx, input.PublicIPv4)
	if err != nil || len(names) == 0 {
		return CheckResult{
			Name: name, Status: CheckFailed, Severity: SeverityCritical,
			Detail: fmt.Sprintf("no PTR record for %s", input.PublicIPv4),
			Remedy: "set the PTR in the hosting provider's panel; reverse delegation belongs to the owner of the IP block, not to the DNS provider",
		}
	}

	for _, candidate := range names {
		if strings.EqualFold(strings.TrimSuffix(candidate, "."), input.PrimaryMailHost) {
			return passed(name, SeverityCritical, "")
		}
	}

	return CheckResult{
		Name: name, Status: CheckFailed, Severity: SeverityCritical,
		Detail: fmt.Sprintf("PTR of %s is %v, want %s", input.PublicIPv4, names, input.PrimaryMailHost),
		Remedy: "point the PTR at " + input.PrimaryMailHost + " in the hosting provider's panel",
	}
}

func (uc *PreflightUseCase) checkForwardConfirmedReverseDNS(ctx context.Context, input PreflightInput) CheckResult {
	const name = "FCrDNS: the mail host resolves back to the public IP"

	if uc.deps.LookupHost == nil || input.PublicIPv4 == "" {
		return skipped(name, SeverityCritical)
	}

	addresses, err := uc.deps.LookupHost(ctx, input.PrimaryMailHost)
	if err != nil {
		return CheckResult{
			Name: name, Status: CheckFailed, Severity: SeverityCritical,
			Detail: fmt.Sprintf("cannot resolve %s: %v", input.PrimaryMailHost, err),
			Remedy: "publish the A record for " + input.PrimaryMailHost,
		}
	}

	for _, address := range addresses {
		if address == input.PublicIPv4 {
			return passed(name, SeverityCritical, "")
		}
	}

	return CheckResult{
		Name: name, Status: CheckFailed, Severity: SeverityCritical,
		Detail: fmt.Sprintf("%s resolves to %v, not to %s", input.PrimaryMailHost, addresses, input.PublicIPv4),
		Remedy: "make the A record and the PTR agree; Gmail and Outlook reject or spam-file mail without forward-confirmed reverse DNS",
	}
}

func (uc *PreflightUseCase) checkBlocklists(ctx context.Context, input PreflightInput) CheckResult {
	const name = "Public IP is not on a major blocklist"

	if uc.deps.LookupTXT == nil || input.PublicIPv4 == "" {
		return skipped(name, SeverityCritical)
	}

	reversed := reverseIPv4(input.PublicIPv4)
	if reversed == "" {
		return skipped(name, SeverityCritical)
	}

	var listed []string
	// Sorted so the report is stable between runs.
	zones := make([]string, 0, len(dnsblZones))
	for zone := range dnsblZones {
		zones = append(zones, zone)
	}
	sort.Strings(zones)

	for _, zone := range zones {
		if records, err := uc.deps.LookupTXT(ctx, reversed+"."+zone); err == nil && len(records) > 0 {
			listed = append(listed, dnsblZones[zone])
		}
	}

	if len(listed) > 0 {
		return CheckResult{
			Name: name, Status: CheckFailed, Severity: SeverityCritical,
			Detail: fmt.Sprintf("%s is listed on: %s", input.PublicIPv4, strings.Join(listed, ", ")),
			Remedy: "request delisting with each provider before sending, or move to an IP with clean reputation",
		}
	}
	return passed(name, SeverityCritical, "")
}

func (uc *PreflightUseCase) checkCertificate(input PreflightInput) CheckResult {
	const name = "A TLS certificate is loaded for the mail host"

	if uc.deps.HasCertificateFor == nil {
		return skipped(name, SeverityCritical)
	}
	if uc.deps.HasCertificateFor(input.PrimaryMailHost) {
		return passed(name, SeverityCritical, "")
	}

	return CheckResult{
		Name: name, Status: CheckFailed, Severity: SeverityCritical,
		Detail: fmt.Sprintf("no certificate for %s", input.PrimaryMailHost),
		Remedy: "Traefik only requests certificates for hosts it routes; declare an HTTP router with Host(`" + input.PrimaryMailHost + "`) so ACME issues one",
	}
}

func (uc *PreflightUseCase) checkDaneRequiresAcmeMode(input PreflightInput) CheckResult {
	const name = "DANE is only enabled in self-managed ACME mode"

	if !input.DaneEnabled {
		return CheckResult{
			Name: name, Status: CheckSkipped, Severity: SeverityCritical,
			Detail: "DANE is disabled",
		}
	}
	if strings.EqualFold(input.TLSMode, "acme") {
		return passed(name, SeverityCritical, "")
	}

	return CheckResult{
		Name: name, Status: CheckFailed, Severity: SeverityCritical,
		Detail: "DANE is enabled while certificates come from Traefik",
		Remedy: "Traefik cannot sign a CSR with a pre-generated key, so the TLSA record would stop matching at the next renewal and every validating MTA would reject mail; set TLS_MODE=acme or disable DANE",
	}
}

func (uc *PreflightUseCase) checkMtaStsEndpoint(ctx context.Context, input PreflightInput) CheckResult {
	const name = "MTA-STS policy is served over HTTPS with a valid certificate"

	if uc.deps.FetchMtaSts == nil || input.MailDomain == "" {
		return skipped(name, SeverityHigh)
	}
	if err := uc.deps.FetchMtaSts(ctx, input.MailDomain); err != nil {
		return CheckResult{
			Name: name, Status: CheckFailed, Severity: SeverityHigh,
			Detail: fmt.Sprintf("cannot fetch the policy for %s: %v", input.MailDomain, err),
			Remedy: "publish the mta-sts host and route it through the proxy with a valid certificate; senders that cannot fetch the policy fall back to unprotected delivery",
		}
	}
	return passed(name, SeverityHigh, "")
}

func (uc *PreflightUseCase) checkClockDrift(ctx context.Context) CheckResult {
	const name = "System clock is synchronised"
	const maxDrift = 5 * time.Second

	if uc.deps.NetworkTime == nil || uc.deps.Now == nil {
		return skipped(name, SeverityHigh)
	}

	networkTime, err := uc.deps.NetworkTime(ctx)
	if err != nil {
		return skipped(name, SeverityHigh)
	}

	drift := uc.deps.Now().Sub(networkTime)
	if drift < 0 {
		drift = -drift
	}
	if drift > maxDrift {
		return CheckResult{
			Name: name, Status: CheckFailed, Severity: SeverityHigh,
			Detail: fmt.Sprintf("local clock is off by %v", drift.Round(time.Millisecond)),
			Remedy: "enable NTP on the host; DKIM signature timestamps and TOTP codes both depend on an accurate clock",
		}
	}
	return passed(name, SeverityHigh, fmt.Sprintf("drift %v", drift.Round(time.Millisecond)))
}

func (uc *PreflightUseCase) checkDiskSpace() CheckResult {
	const name = "Mail spool has more than 20% free space"

	if uc.deps.FreeDiskPercent == nil {
		return skipped(name, SeverityMedium)
	}

	free, err := uc.deps.FreeDiskPercent()
	if err != nil {
		return skipped(name, SeverityMedium)
	}
	if free < 20 {
		return CheckResult{
			Name: name, Status: CheckFailed, Severity: SeverityMedium,
			Detail: fmt.Sprintf("%.1f%% free", free),
			Remedy: "free space or grow the volume; a full spool means accepted mail cannot be written and has to be refused",
		}
	}
	return passed(name, SeverityMedium, fmt.Sprintf("%.1f%% free", free))
}

func (uc *PreflightUseCase) checkIPv6Coherence(ctx context.Context, input PreflightInput) CheckResult {
	const name = "IPv6 is either fully configured or disabled"

	if input.PublicIPv6 == "" {
		return passed(name, SeverityMedium, "IPv6 is disabled")
	}
	if uc.deps.LookupAddr == nil {
		return skipped(name, SeverityMedium)
	}

	names, err := uc.deps.LookupAddr(ctx, input.PublicIPv6)
	if err != nil || len(names) == 0 {
		return CheckResult{
			Name: name, Status: CheckFailed, Severity: SeverityMedium,
			Detail: fmt.Sprintf("no PTR for %s", input.PublicIPv6),
			Remedy: "publish a PTR for the IPv6 address or unset PUBLIC_IPV6; half-configured IPv6 is a common cause of rejection",
		}
	}
	return passed(name, SeverityMedium, "")
}

func passed(name string, severity Severity, detail string) CheckResult {
	return CheckResult{Name: name, Status: CheckPassed, Severity: severity, Detail: detail}
}

func skipped(name string, severity Severity) CheckResult {
	return CheckResult{
		Name: name, Status: CheckSkipped, Severity: severity,
		Detail: "not enough configuration to run this check",
	}
}

// reverseIPv4 turns "192.0.2.10" into "10.2.0.192", the form DNSBL queries
// take. It returns an empty string for anything that is not an IPv4 address.
func reverseIPv4(address string) string {
	ip := net.ParseIP(address).To4()
	if ip == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", ip[3], ip[2], ip[1], ip[0])
}
