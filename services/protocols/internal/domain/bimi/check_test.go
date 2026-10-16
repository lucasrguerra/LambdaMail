package bimi

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeDNS map[string][]string

func (f fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	records, ok := f[name]
	if !ok {
		return nil, errors.New("no such host")
	}
	return records, nil
}

func fakeFetch(files map[string]Response) Fetcher {
	return func(_ context.Context, url string) (Response, error) {
		resp, ok := files[url]
		if !ok {
			return Response{}, errors.New("connection refused")
		}
		return resp, nil
	}
}

func byID(checks []Check) map[string]Check {
	out := map[string]Check{}
	for _, c := range checks {
		out[c.ID] = c
	}
	return out
}

const (
	logoURL = "https://mail.ebay.com/.well-known/bimi/default.svg"
	vmcURL  = "https://mail.ebay.com/.well-known/bimi/vmc.pem"
)

// goodSVG is a logo the validator accepts.
const goodSVG = `<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps" viewBox="0 0 100 100"><title>Example</title><rect width="100" height="100" fill="#fff"/></svg>`

func TestRunChecksAllGoodWithTheRealCertificate(t *testing.T) {
	svg := read(t, "ebay.svg")
	pemData := read(t, "ebay.pem")
	in := Inputs{Domain: "ebay.com", LogoURL: logoURL, CertificateURL: vmcURL, SVG: svg, CertificatePEM: pemData, Now: fixtureNow}
	dns := fakeDNS{
		"_dmarc.ebay.com":        {"v=DMARC1; p=reject; pct=100; rua=mailto:x@ebay.com"},
		"default._bimi.ebay.com": {"v=BIMI1; l=" + logoURL + "; a=" + vmcURL + ";"},
	}
	fetch := fakeFetch(map[string]Response{
		logoURL: {Status: 200, ContentType: "image/svg+xml", Body: svg},
		vmcURL:  {Status: 200, ContentType: "application/x-pem-file", Body: pemData},
	})

	got := byID(RunChecks(context.Background(), in, dns, fetch))
	for _, id := range []string{"logo", "dmarc", "record", "logoUrl", "certificate", "certificateUrl"} {
		if got[id].Status != StatusOK {
			t.Errorf("%s = %s (%s)", id, got[id].Status, got[id].Detail)
		}
	}
}

func TestRunChecksDMARC(t *testing.T) {
	cases := []struct {
		record string
		want   Status
	}{
		{"v=DMARC1; p=none", StatusFail},
		{"v=DMARC1; p=quarantine", StatusOK},
		{"v=DMARC1; p=reject; pct=100", StatusOK},
		{"v=DMARC1; p=reject; pct=50", StatusWarn},
		{"v=spf1 -all", StatusFail},
	}
	for _, c := range cases {
		got := checkDMARC(context.Background(), "example.test", fakeDNS{"_dmarc.example.test": {c.record}})
		if got.Status != c.want {
			t.Errorf("%q: %s (%s), want %s", c.record, got.Status, got.Detail, c.want)
		}
	}
	if got := checkDMARC(context.Background(), "example.test", fakeDNS{}); got.Status != StatusFail {
		t.Errorf("missing record: %s", got.Status)
	}
}

func TestRunChecksRecordMismatchesAreSpelledOut(t *testing.T) {
	in := Inputs{Domain: "example.test", LogoURL: "https://mail.example.test/.well-known/bimi/default.svg", CertificateURL: "https://mail.example.test/.well-known/bimi/vmc.pem"}

	missing := checkRecord(context.Background(), in, fakeDNS{})
	if missing.Status != StatusFail || !strings.Contains(missing.Detail, "default._bimi.example.test") {
		t.Errorf("missing: %+v", missing)
	}

	wrong := checkRecord(context.Background(), in, fakeDNS{"default._bimi.example.test": {"v=BIMI1; l=https://elsewhere.test/logo.svg;"}})
	if wrong.Status != StatusFail || !strings.Contains(wrong.Detail, "l=") || !strings.Contains(wrong.Detail, "a=") {
		t.Errorf("wrong: %+v", wrong)
	}

	// Without a certificate here, an a= in DNS is a fault too: it points nowhere
	noCert := Inputs{Domain: "example.test", LogoURL: in.LogoURL}
	stray := checkRecord(context.Background(), noCert, fakeDNS{"default._bimi.example.test": {"v=BIMI1; l=" + in.LogoURL + "; a=https://old.test/x.pem"}})
	if stray.Status != StatusFail {
		t.Errorf("stray a=: %+v", stray)
	}
}

func TestRunChecksLogoURL(t *testing.T) {
	in := Inputs{Domain: "example.test", LogoURL: logoURL, SVG: []byte(goodSVG)}
	cases := map[string]struct {
		resp Response
		err  bool
		want Status
	}{
		"ok":              {Response{Status: 200, ContentType: "image/svg+xml; charset=utf-8", Body: []byte(goodSVG)}, false, StatusOK},
		"not found":       {Response{Status: 404}, false, StatusFail},
		"wrong type":      {Response{Status: 200, ContentType: "text/html", Body: []byte(goodSVG)}, false, StatusFail},
		"different bytes": {Response{Status: 200, ContentType: "image/svg+xml", Body: []byte("<svg/>")}, false, StatusFail},
		"unreachable":     {Response{}, true, StatusFail},
	}
	for name, c := range cases {
		files := map[string]Response{}
		if !c.err {
			files[logoURL] = c.resp
		}
		if got := checkLogoURL(context.Background(), in, fakeFetch(files)); got.Status != c.want {
			t.Errorf("%s: %s (%s), want %s", name, got.Status, got.Detail, c.want)
		}
	}
}

func TestRunChecksCertificate(t *testing.T) {
	svg := read(t, "ebay.svg")
	pemData := read(t, "ebay.pem")
	base := Inputs{Domain: "ebay.com", LogoURL: logoURL, SVG: svg, Now: fixtureNow}

	t.Run("none is a warning, with the reason", func(t *testing.T) {
		got := byID(checkCertificate(context.Background(), base, fakeFetch(nil)))["certificate"]
		if got.Status != StatusWarn || !strings.Contains(got.Detail, "Gmail") {
			t.Errorf("%+v", got)
		}
	})

	t.Run("hosted but the public URL serves something else", func(t *testing.T) {
		in := base
		in.CertificatePEM, in.CertificateURL = pemData, vmcURL
		got := byID(checkCertificate(context.Background(), in, fakeFetch(map[string]Response{vmcURL: {Status: 200, Body: []byte("other")}})))
		if got["certificate"].Status != StatusOK || got["certificateUrl"].Status != StatusFail {
			t.Errorf("%+v", got)
		}
	})

	t.Run("hosted for a logo that changed since", func(t *testing.T) {
		in := base
		in.CertificatePEM, in.CertificateURL, in.SVG = pemData, vmcURL, []byte(goodSVG)
		got := byID(checkCertificate(context.Background(), in, fakeFetch(map[string]Response{vmcURL: {Status: 200, Body: pemData}})))
		if got["certificate"].Status != StatusFail || !strings.Contains(got["certificate"].Detail, "logoMismatch") {
			t.Errorf("%+v", got["certificate"])
		}
	})

	t.Run("kept elsewhere, fetched and inspected", func(t *testing.T) {
		in := base
		in.ExternalCertificateURL = "https://vmc.example.test/x.pem"
		got := byID(checkCertificate(context.Background(), in, fakeFetch(map[string]Response{in.ExternalCertificateURL: {Status: 200, Body: pemData}})))
		if got["certificate"].Status != StatusOK {
			t.Errorf("%+v", got["certificate"])
		}
		bad := byID(checkCertificate(context.Background(), in, fakeFetch(nil)))
		if bad["certificate"].Status != StatusFail {
			t.Errorf("unreachable external certificate: %+v", bad["certificate"])
		}
	})
}
