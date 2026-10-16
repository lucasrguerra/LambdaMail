package bimi

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
)

// What decides whether a receiver shows the logo is spread over six places that
// each fail silently: the logo file, the DMARC policy, the BIMI DNS record, the
// public URL of the logo, the certificate, and the public URL of the
// certificate. RunChecks looks at all of them the way a receiver would and says
// which one is wrong, instead of leaving the operator to wait for a message to
// arrive somewhere and show nothing.

// Status of one check.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is the outcome of one step.
type Check struct {
	ID     string
	Status Status
	Detail string
}

// Resolver looks up TXT records.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Response is what a fetch returned.
type Response struct {
	Status      int
	ContentType string
	Body        []byte
}

// Fetcher gets a public HTTPS URL.
type Fetcher func(ctx context.Context, url string) (Response, error)

// Inputs describe what should be published.
type Inputs struct {
	Domain string
	// LogoURL and CertificateURL are where the DNS record is expected to
	// point. CertificateURL is empty when no certificate is published.
	LogoURL        string
	CertificateURL string
	SVG            []byte
	// CertificatePEM is the chain held here, if any. ExternalCertificateURL is
	// the one the operator keeps elsewhere, used only when none is held.
	CertificatePEM         []byte
	ExternalCertificateURL string
	Now                    time.Time
}

// RunChecks runs every check and returns them in the order a receiver would
// meet them. It never fails as a whole: a lookup that errors is a check that
// fails, with the reason.
func RunChecks(ctx context.Context, in Inputs, dns Resolver, fetch Fetcher) []Check {
	checks := []Check{
		checkLogo(in),
		checkDMARC(ctx, in.Domain, dns),
		checkRecord(ctx, in, dns),
		checkLogoURL(ctx, in, fetch),
	}
	checks = append(checks, checkCertificate(ctx, in, fetch)...)
	return checks
}

func checkLogo(in Inputs) Check {
	violations := Validate(in.SVG)
	if len(violations) == 0 {
		return Check{ID: "logo", Status: StatusOK, Detail: fmt.Sprintf("SVG Tiny PS valid, %d bytes", len(in.SVG))}
	}
	reasons := make([]string, 0, len(violations))
	for _, v := range violations {
		reasons = append(reasons, v.String())
	}
	return Check{ID: "logo", Status: StatusFail, Detail: strings.Join(reasons, "; ")}
}

// checkDMARC: BIMI is only honoured for a domain that enforces DMARC. A policy
// of "none" is the common reason a correct logo and certificate show nothing.
func checkDMARC(ctx context.Context, domain string, dns Resolver) Check {
	records, err := dns.LookupTXT(ctx, "_dmarc."+domain)
	if err != nil {
		return Check{ID: "dmarc", Status: StatusFail, Detail: fmt.Sprintf("no DMARC record at _dmarc.%s (%v)", domain, err)}
	}
	var record string
	for _, r := range records {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r)), "v=dmarc1") {
			record = r
			break
		}
	}
	if record == "" {
		return Check{ID: "dmarc", Status: StatusFail, Detail: "no DMARC record: BIMI needs a policy of quarantine or reject"}
	}

	tags := parseTags(record)
	policy := strings.ToLower(tags["p"])
	switch policy {
	case "quarantine", "reject":
	default:
		return Check{ID: "dmarc", Status: StatusFail, Detail: fmt.Sprintf("DMARC policy is %q: BIMI needs quarantine or reject", policy)}
	}
	if pct, ok := tags["pct"]; ok && strings.TrimSpace(pct) != "100" {
		return Check{ID: "dmarc", Status: StatusWarn, Detail: fmt.Sprintf("DMARC applies to only %s%% of mail; BIMI needs pct=100 (or no pct)", pct)}
	}
	return Check{ID: "dmarc", Status: StatusOK, Detail: "DMARC policy is " + policy}
}

// checkRecord compares the published BIMI record with the one the service
// would publish.
func checkRecord(ctx context.Context, in Inputs, dns Resolver) Check {
	name := "default._bimi." + in.Domain
	records, err := dns.LookupTXT(ctx, name)
	if err != nil {
		return Check{ID: "record", Status: StatusFail, Detail: fmt.Sprintf("no TXT record at %s (%v)", name, err)}
	}
	var found map[string]string
	for _, r := range records {
		tags := parseTags(r)
		if strings.EqualFold(tags["v"], "BIMI1") {
			found = tags
			break
		}
	}
	if found == nil {
		return Check{ID: "record", Status: StatusFail, Detail: fmt.Sprintf("%s has no v=BIMI1 record", name)}
	}

	var problems []string
	if found["l"] != in.LogoURL {
		problems = append(problems, fmt.Sprintf("l= is %q, expected %q", found["l"], in.LogoURL))
	}
	if found["a"] != in.CertificateURL {
		if in.CertificateURL == "" {
			problems = append(problems, fmt.Sprintf("a= is %q but no certificate is published here", found["a"]))
		} else {
			problems = append(problems, fmt.Sprintf("a= is %q, expected %q", found["a"], in.CertificateURL))
		}
	}
	if len(problems) > 0 {
		return Check{ID: "record", Status: StatusFail, Detail: strings.Join(problems, "; ")}
	}
	return Check{ID: "record", Status: StatusOK, Detail: "the BIMI record points at this logo" + ifs(in.CertificateURL != "", " and certificate")}
}

func checkLogoURL(ctx context.Context, in Inputs, fetch Fetcher) Check {
	resp, err := fetch(ctx, in.LogoURL)
	if err != nil {
		return Check{ID: "logoUrl", Status: StatusFail, Detail: fmt.Sprintf("could not fetch %s (%v)", in.LogoURL, err)}
	}
	switch {
	case resp.Status != 200:
		return Check{ID: "logoUrl", Status: StatusFail, Detail: fmt.Sprintf("%s answered %d", in.LogoURL, resp.Status)}
	case !strings.HasPrefix(strings.ToLower(resp.ContentType), "image/svg+xml"):
		return Check{ID: "logoUrl", Status: StatusFail, Detail: fmt.Sprintf("served as %q, receivers require image/svg+xml", resp.ContentType)}
	case !bytes.Equal(resp.Body, in.SVG):
		return Check{ID: "logoUrl", Status: StatusFail, Detail: "the public URL serves a different file than the one stored (a cache in front?)"}
	}
	return Check{ID: "logoUrl", Status: StatusOK, Detail: "the logo is public over HTTPS and matches"}
}

// checkCertificate returns one or two checks: the certificate itself, and for a
// hosted one, that the public URL really serves it.
func checkCertificate(ctx context.Context, in Inputs, fetch Fetcher) []Check {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	var pemData []byte
	var out []Check

	switch {
	case len(in.CertificatePEM) > 0:
		pemData = in.CertificatePEM
		if in.CertificateURL != "" {
			out = append(out, checkCertificateURL(ctx, in, fetch))
		}
	case in.ExternalCertificateURL != "":
		resp, err := fetch(ctx, in.ExternalCertificateURL)
		if err != nil || resp.Status != 200 {
			detail := fmt.Sprintf("could not fetch %s", in.ExternalCertificateURL)
			if err != nil {
				detail += fmt.Sprintf(" (%v)", err)
			} else {
				detail += fmt.Sprintf(": it answered %d", resp.Status)
			}
			return []Check{{ID: "certificate", Status: StatusFail, Detail: detail}}
		}
		pemData = resp.Body
	default:
		return []Check{{
			ID:     "certificate",
			Status: StatusWarn,
			Detail: "no certificate: Gmail and Apple Mail show the logo only with a VMC or CMC from a certificate authority; some other receivers show it without one",
		}}
	}

	cert, violations := InspectCertificate(pemData, in.Domain, in.SVG, now)
	if len(violations) > 0 {
		reasons := make([]string, 0, len(violations))
		for _, v := range violations {
			reasons = append(reasons, v.String())
		}
		return append([]Check{{ID: "certificate", Status: StatusFail, Detail: strings.Join(reasons, "; ")}}, out...)
	}
	if len(cert.Warnings) > 0 {
		return append([]Check{{ID: "certificate", Status: StatusWarn, Detail: strings.Join(cert.Warnings, "; ")}}, out...)
	}
	detail := fmt.Sprintf("%s issued to %s, valid until %s, and it carries this logo", strings.ToUpper(string(cert.Kind)), cert.Organization, cert.NotAfter.UTC().Format("2006-01-02"))
	return append([]Check{{ID: "certificate", Status: StatusOK, Detail: detail}}, out...)
}

func checkCertificateURL(ctx context.Context, in Inputs, fetch Fetcher) Check {
	resp, err := fetch(ctx, in.CertificateURL)
	switch {
	case err != nil:
		return Check{ID: "certificateUrl", Status: StatusFail, Detail: fmt.Sprintf("could not fetch %s (%v)", in.CertificateURL, err)}
	case resp.Status != 200:
		return Check{ID: "certificateUrl", Status: StatusFail, Detail: fmt.Sprintf("%s answered %d", in.CertificateURL, resp.Status)}
	case !bytes.Equal(bytes.TrimSpace(resp.Body), bytes.TrimSpace(in.CertificatePEM)):
		return Check{ID: "certificateUrl", Status: StatusFail, Detail: "the public URL serves a different file than the one stored"}
	}
	return Check{ID: "certificateUrl", Status: StatusOK, Detail: "the certificate is public over HTTPS and matches"}
}

// parseTags reads "k=v; k=v" records (DMARC, BIMI).
func parseTags(record string) map[string]string {
	tags := map[string]string{}
	for _, part := range strings.Split(record, ";") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if found {
			tags[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	return tags
}

func ifs(condition bool, text string) string {
	if condition {
		return text
	}
	return ""
}
