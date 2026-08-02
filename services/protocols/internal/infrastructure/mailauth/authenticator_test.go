package mailauth

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	dkimlib "github.com/emersion/go-msgauth/dkim"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/infrastructure/dkim"
)

// fakeZone answers TXT and address lookups from a fixed map, so the whole
// authentication chain runs without touching the real internet.
type fakeZone struct {
	txt map[string][]string
	a   map[string][]string
}

func (z *fakeZone) LookupTXT(_ context.Context, name string) ([]string, error) {
	return z.lookupTXT(name)
}

func (z *fakeZone) lookupTXT(name string) ([]string, error) {
	name = strings.TrimSuffix(name, ".")
	if records, ok := z.txt[name]; ok {
		return records, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (z *fakeZone) LookupIPAddr(_ context.Context, name string) ([]net.IPAddr, error) {
	name = strings.TrimSuffix(name, ".")
	var out []net.IPAddr
	for _, ip := range z.a[name] {
		out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
	}
	if len(out) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	return out, nil
}

func (z *fakeZone) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (z *fakeZone) LookupAddr(_ context.Context, addr string) ([]string, error) {
	return nil, &net.DNSError{Err: "no such host", Name: addr, IsNotFound: true}
}

func newTestAuthenticator(zone *fakeZone) *Authenticator {
	return NewAuthenticator("mail.lambdamail.test").WithLookupTXT(zone.lookupTXT, zone)
}

const plainMessage = "From: sender@sender.test\r\n" +
	"To: rcpt@lambdamail.test\r\n" +
	"Subject: hello\r\n" +
	"Date: Mon, 20 Jul 2026 10:00:00 +0000\r\n" +
	"Message-ID: <m1@sender.test>\r\n" +
	"\r\n" +
	"body\r\n"

// A sender whose SPF authorises the connecting IP, with a DMARC record in
// place, must come out as pass/pass on the aligned identifier.
func TestAuthenticate_SpfPassAlignsDmarc(t *testing.T) {
	zone := &fakeZone{
		txt: map[string][]string{
			"sender.test":        {"v=spf1 ip4:192.0.2.10 -all"},
			"_dmarc.sender.test": {"v=DMARC1; p=reject"},
		},
	}

	got := newTestAuthenticator(zone).Authenticate(context.Background(), port.InboundAuthInput{
		ClientIP:     net.ParseIP("192.0.2.10"),
		HeloDomain:   "mx.sender.test",
		EnvelopeFrom: "sender@sender.test",
		Message:      []byte(plainMessage),
	})

	if got.SPF != port.AuthResultPass {
		t.Errorf("SPF = %q, want pass", got.SPF)
	}
	if got.DMARC != port.AuthResultPass {
		t.Errorf("DMARC = %q, want pass (SPF aligned with the From: domain)", got.DMARC)
	}
	if got.DmarcPolicy != "reject" {
		t.Errorf("DmarcPolicy = %q, want reject", got.DmarcPolicy)
	}
	if !strings.Contains(got.AuthenticationResults, "spf=pass") ||
		!strings.Contains(got.AuthenticationResults, "dmarc=pass") {
		t.Errorf("Authentication-Results does not report the verdicts: %s", got.AuthenticationResults)
	}
	if !strings.HasPrefix(got.AuthenticationResults, "mail.lambdamail.test") {
		t.Errorf("Authentication-Results must name this server first: %s", got.AuthenticationResults)
	}
}

// An unauthorised IP must fail SPF, and with no DKIM signature to fall back
// on, DMARC fails too - which is what lets the reject policy be honoured.
func TestAuthenticate_SpfFailWithoutDkimFailsDmarc(t *testing.T) {
	zone := &fakeZone{
		txt: map[string][]string{
			"sender.test":        {"v=spf1 ip4:192.0.2.10 -all"},
			"_dmarc.sender.test": {"v=DMARC1; p=quarantine"},
		},
	}

	got := newTestAuthenticator(zone).Authenticate(context.Background(), port.InboundAuthInput{
		ClientIP:     net.ParseIP("198.51.100.66"),
		HeloDomain:   "evil.test",
		EnvelopeFrom: "sender@sender.test",
		Message:      []byte(plainMessage),
	})

	if got.SPF != port.AuthResultFail {
		t.Errorf("SPF = %q, want fail", got.SPF)
	}
	if got.DMARC != port.AuthResultFail {
		t.Errorf("DMARC = %q, want fail", got.DMARC)
	}
	if got.DmarcPolicy != "quarantine" {
		t.Errorf("DmarcPolicy = %q, want quarantine", got.DmarcPolicy)
	}
}

// A valid DKIM signature must rescue DMARC even when SPF fails, which is
// exactly the forwarding case DKIM exists for.
func TestAuthenticate_DkimPassAlignsDmarcDespiteSpfFail(t *testing.T) {
	generated, err := dkim.Generate(dkim.AlgorithmRSA2048)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	signer, err := dkim.ParsePrivateKey(generated.PrivateKeyPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}

	var signed bytes.Buffer
	err = dkimlib.Sign(&signed, strings.NewReader(plainMessage), &dkimlib.SignOptions{
		Domain:                 "sender.test",
		Selector:               "default",
		Signer:                 signer,
		HeaderCanonicalization: dkimlib.CanonicalizationRelaxed,
		BodyCanonicalization:   dkimlib.CanonicalizationRelaxed,
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	zone := &fakeZone{
		txt: map[string][]string{
			"sender.test":                    {"v=spf1 ip4:192.0.2.10 -all"},
			"_dmarc.sender.test":             {"v=DMARC1; p=reject"},
			"default._domainkey.sender.test": {"v=DKIM1; k=rsa; p=" + generated.PublicKeyBase64},
		},
	}

	got := newTestAuthenticator(zone).Authenticate(context.Background(), port.InboundAuthInput{
		ClientIP:     net.ParseIP("203.0.113.99"), // forwarder, not in SPF
		HeloDomain:   "forwarder.test",
		EnvelopeFrom: "sender@sender.test",
		Message:      signed.Bytes(),
	})

	if got.DKIM != port.AuthResultPass {
		t.Fatalf("DKIM = %q, want pass", got.DKIM)
	}
	if got.SPF == port.AuthResultPass {
		t.Fatalf("SPF unexpectedly passed; the test cannot prove DKIM rescued DMARC")
	}
	if got.DMARC != port.AuthResultPass {
		t.Errorf("DMARC = %q, want pass via the aligned DKIM signature", got.DMARC)
	}
}

// A tampered body must invalidate the signature.
func TestAuthenticate_TamperedBodyFailsDkim(t *testing.T) {
	generated, _ := dkim.Generate(dkim.AlgorithmRSA2048)
	signer, _ := dkim.ParsePrivateKey(generated.PrivateKeyPEM)

	var signed bytes.Buffer
	dkimlib.Sign(&signed, strings.NewReader(plainMessage), &dkimlib.SignOptions{
		Domain: "sender.test", Selector: "default", Signer: signer,
		HeaderCanonicalization: dkimlib.CanonicalizationRelaxed,
		BodyCanonicalization:   dkimlib.CanonicalizationRelaxed,
	})

	tampered := bytes.Replace(signed.Bytes(), []byte("body"), []byte("evil"), 1)

	zone := &fakeZone{txt: map[string][]string{
		"default._domainkey.sender.test": {"v=DKIM1; k=rsa; p=" + generated.PublicKeyBase64},
	}}

	got := newTestAuthenticator(zone).Authenticate(context.Background(), port.InboundAuthInput{
		ClientIP: net.ParseIP("203.0.113.99"),
		Message:  tampered,
	})

	if got.DKIM != port.AuthResultFail {
		t.Errorf("DKIM = %q, want fail for a tampered body", got.DKIM)
	}
}

// A domain publishing no DMARC record yields "none", never a hard failure.
func TestAuthenticate_NoDmarcRecordYieldsNone(t *testing.T) {
	zone := &fakeZone{txt: map[string][]string{"sender.test": {"v=spf1 -all"}}}

	got := newTestAuthenticator(zone).Authenticate(context.Background(), port.InboundAuthInput{
		ClientIP:     net.ParseIP("198.51.100.1"),
		EnvelopeFrom: "sender@sender.test",
		Message:      []byte(plainMessage),
	})

	if got.DMARC != port.AuthResultNone {
		t.Errorf("DMARC = %q, want none", got.DMARC)
	}
	if got.DmarcPolicy != "" {
		t.Errorf("DmarcPolicy = %q, want empty", got.DmarcPolicy)
	}
}

// A bounce arrives with an empty envelope sender; RFC 7208 section 2.4 says
// the HELO identity is checked instead, and it must not crash.
func TestAuthenticate_EmptyEnvelopeFromChecksHelo(t *testing.T) {
	zone := &fakeZone{txt: map[string][]string{
		"mx.sender.test": {"v=spf1 ip4:192.0.2.10 -all"},
	}}

	got := newTestAuthenticator(zone).Authenticate(context.Background(), port.InboundAuthInput{
		ClientIP:     net.ParseIP("192.0.2.10"),
		HeloDomain:   "mx.sender.test",
		EnvelopeFrom: "",
		Message:      []byte(plainMessage),
	})

	if got.SPF != port.AuthResultPass {
		t.Errorf("SPF = %q, want pass against the HELO identity", got.SPF)
	}
}
