package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"lambdamail/protocols/internal/application/usecase"
	tlsprovider "lambdamail/protocols/internal/infrastructure/tls"
)

// runPreflight executes the environment checks of PLAN.md section 15 and
// exits non-zero when a critical one fails, so the deploy pipeline can gate
// on it.
func runPreflight(cfg config) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	report := usecase.NewPreflightUseCase(buildPreflightDeps(cfg)).Execute(ctx, usecase.PreflightInput{
		PrimaryMailHost: cfg.PrimaryMailHost,
		MailDomain:      cfg.domain(),
		PublicIPv4:      cfg.PublicIPv4,
		PublicIPv6:      cfg.PublicIPv6,
		DaneEnabled:     cfg.OutboundDane,
		TLSMode:         cfg.TLSMode,
		SmarthostSet:    cfg.RelayHost != "",
	})

	fmt.Print(report)

	if report.Blocking() {
		fmt.Fprintln(os.Stderr, "\npreflight FAILED: at least one critical check did not pass")
		os.Exit(1)
	}
	fmt.Println("\npreflight passed")
}

func buildPreflightDeps(cfg config) usecase.PreflightDeps {
	resolver := net.DefaultResolver

	deps := usecase.PreflightDeps{
		DialPort25: func(ctx context.Context, address string) error {
			dialer := net.Dialer{Timeout: 10 * time.Second}
			conn, err := dialer.DialContext(ctx, "tcp", address)
			if err != nil {
				return err
			}
			return conn.Close()
		},
		LookupAddr: resolver.LookupAddr,
		LookupHost: resolver.LookupHost,
		LookupTXT:  resolver.LookupTXT,
		FetchMtaSts: func(ctx context.Context, domain string) error {
			url := fmt.Sprintf("https://mta-sts.%s/.well-known/mta-sts.txt", domain)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			// The certificate is validated on purpose: RFC 8461 section 3.3
			// requires the policy to be served under a valid certificate for
			// the mta-sts host, and a sender that cannot validate it ignores
			// the policy entirely.
			resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("HTTP %d", resp.StatusCode)
			}
			return nil
		},
		FreeDiskPercent: func() (float64, error) { return freeDiskPercent(cfg.SpoolDir) },
		Now:             time.Now,
		NetworkTime:     httpDateTime,
	}

	// The certificate check only means something when a store is configured;
	// otherwise it stays skipped rather than reporting a false pass.
	if cfg.TraefikAcmeDir != "" {
		if watcher, err := tlsprovider.NewAcmeCertWatcher(cfg.TraefikAcmeDir, cfg.TraefikAcmeFile, cfg.PrimaryMailHost); err == nil {
			deps.HasCertificateFor = watcher.HasCertificateFor
		}
	}

	return deps
}

// httpDateTime reads a trusted clock from the Date header of a well-known
// HTTPS endpoint. It is not NTP, but it is accurate to about a second, which
// is enough to catch the drift that breaks DKIM and TOTP.
func httpDateTime(ctx context.Context) (time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://www.cloudflare.com/", nil)
	if err != nil {
		return time.Time{}, err
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()

	dateHeader := resp.Header.Get("Date")
	if dateHeader == "" {
		return time.Time{}, fmt.Errorf("no Date header in the response")
	}
	return http.ParseTime(dateHeader)
}
