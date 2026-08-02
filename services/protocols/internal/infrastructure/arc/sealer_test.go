package arc

import (
	"context"
	"crypto"
	"fmt"
	"strings"
	"testing"
	"time"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/infrastructure/dkim"
)

const forwardedMessage = "From: author@origin.test\r\n" +
	"To: list@forwarder.test\r\n" +
	"Subject: hello\r\n" +
	"Date: Mon, 20 Jul 2026 10:00:00 +0000\r\n" +
	"Message-ID: <m1@origin.test>\r\n" +
	"\r\n" +
	"the body\r\n"

// keyring resolves the public keys of every sealing domain in a test.
type keyring map[string]crypto.PublicKey

func (k keyring) lookup(domain, selector string) (crypto.PublicKey, error) {
	key, ok := k[domain+"/"+selector]
	if !ok {
		return nil, fmt.Errorf("no key for %s/%s", domain, selector)
	}
	return key, nil
}

func newSealer(t *testing.T, domain, algorithm string, ring keyring) *Sealer {
	t.Helper()

	generated, err := dkim.Generate(algorithm)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	signer, err := dkim.ParsePrivateKey(generated.PrivateKeyPEM)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	ring[domain+"/default"] = signer.Public()

	fixed := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	return NewSealer(domain, "default", "mx."+domain, signer).
		WithClock(func() time.Time { return fixed })
}

func passingResult() port.InboundAuthResult {
	return port.InboundAuthResult{
		SPF: "pass", DKIM: "pass", DMARC: "pass",
		AuthenticationResults: "mx.forwarder.test; spf=pass; dkim=pass; dmarc=pass",
	}
}

// A first sealer starts the chain: instance 1 with cv=none
// (RFC 8617 section 5.1.1).
func TestSealer_StartsChainAtInstanceOneWithCvNone(t *testing.T) {
	ring := keyring{}
	sealed, err := newSealer(t, "forwarder.test", dkim.AlgorithmRSA2048, ring).
		Seal(context.Background(), []byte(forwardedMessage), passingResult())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	out := string(sealed)
	for _, name := range []string{HeaderSeal, HeaderMessageSignature, HeaderAuthenticationResults} {
		if !strings.Contains(out, name+":") {
			t.Errorf("missing %s:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "cv=none") {
		t.Errorf("first seal must report cv=none:\n%s", out)
	}
	if !strings.Contains(out, "i=1") {
		t.Errorf("first seal must be instance 1:\n%s", out)
	}
	if !strings.Contains(out, "d=forwarder.test") {
		t.Errorf("seal does not claim the sealing domain:\n%s", out)
	}
	// The original message must survive untouched below the new headers.
	if !strings.HasSuffix(out, forwardedMessage) {
		t.Error("the original message was modified")
	}
}

// The header order is prescribed: the seal is computed last and sits on top.
func TestSealer_WritesHeadersInPrescribedOrder(t *testing.T) {
	ring := keyring{}
	sealed, _ := newSealer(t, "forwarder.test", dkim.AlgorithmRSA2048, ring).
		Seal(context.Background(), []byte(forwardedMessage), passingResult())

	out := string(sealed)
	sealAt := strings.Index(out, HeaderSeal+":")
	amsAt := strings.Index(out, HeaderMessageSignature+":")
	aarAt := strings.Index(out, HeaderAuthenticationResults+":")

	if !(sealAt < amsAt && amsAt < aarAt) {
		t.Errorf("header order is seal=%d ams=%d aar=%d, want seal first", sealAt, amsAt, aarAt)
	}
}

// The whole point: a chain we sealed must verify.
func TestSealer_ProducesVerifiableChain(t *testing.T) {
	for _, algorithm := range []string{dkim.AlgorithmRSA2048, dkim.AlgorithmEd25519} {
		t.Run(algorithm, func(t *testing.T) {
			ring := keyring{}
			sealed, err := newSealer(t, "forwarder.test", algorithm, ring).
				Seal(context.Background(), []byte(forwardedMessage), passingResult())
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}

			state, err := VerifyChain(sealed, ring.lookup)
			if err != nil {
				t.Fatalf("VerifyChain: %v", err)
			}
			if state != ChainPass {
				t.Errorf("chain state = %s, want pass", state)
			}
		})
	}
}

// A second forwarder extends the chain, and both sets must still verify.
func TestSealer_ExtendsExistingChain(t *testing.T) {
	ring := keyring{}

	first, err := newSealer(t, "first.test", dkim.AlgorithmRSA2048, ring).
		Seal(context.Background(), []byte(forwardedMessage), passingResult())
	if err != nil {
		t.Fatalf("first Seal: %v", err)
	}

	second, err := newSealer(t, "second.test", dkim.AlgorithmEd25519, ring).
		Seal(context.Background(), first, passingResult())
	if err != nil {
		t.Fatalf("second Seal: %v", err)
	}

	out := string(second)
	if !strings.Contains(out, "i=2") {
		t.Errorf("the second hop did not take instance 2:\n%s", out)
	}
	// The second hop saw an intact chain, so it reports cv=pass.
	if !strings.Contains(out, "cv=pass") {
		t.Errorf("the second seal should report cv=pass:\n%s", out)
	}
	if count := strings.Count(out, HeaderSeal+":"); count != 2 {
		t.Errorf("found %d seals, want 2", count)
	}

	state, err := VerifyChain(second, ring.lookup)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if state != ChainPass {
		t.Errorf("chain state = %s, want pass", state)
	}
}

// Modifying the body after sealing must break the message signature - this is
// what proves the body hash is actually covering anything.
func TestVerifyChain_DetectsBodyTampering(t *testing.T) {
	ring := keyring{}
	sealed, _ := newSealer(t, "forwarder.test", dkim.AlgorithmRSA2048, ring).
		Seal(context.Background(), []byte(forwardedMessage), passingResult())

	tampered := strings.Replace(string(sealed), "the body", "evil body", 1)

	state, err := VerifyChain([]byte(tampered), ring.lookup)
	if err == nil {
		t.Fatal("expected tampering to be detected")
	}
	if state != ChainFail {
		t.Errorf("chain state = %s, want fail", state)
	}
}

// Modifying a signed header must break the signature too.
func TestVerifyChain_DetectsHeaderTampering(t *testing.T) {
	ring := keyring{}
	sealed, _ := newSealer(t, "forwarder.test", dkim.AlgorithmRSA2048, ring).
		Seal(context.Background(), []byte(forwardedMessage), passingResult())

	tampered := strings.Replace(string(sealed), "Subject: hello", "Subject: forged", 1)

	if state, err := VerifyChain([]byte(tampered), ring.lookup); err == nil || state != ChainFail {
		t.Errorf("state = %s, err = %v; want a detected failure", state, err)
	}
}

// A message with no chain is "none", not a failure.
func TestVerifyChain_NoChainIsNone(t *testing.T) {
	state, err := VerifyChain([]byte(forwardedMessage), keyring{}.lookup)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if state != ChainNone {
		t.Errorf("state = %s, want none", state)
	}
}

// RFC 8617 section 5.1.2: a broken chain must not be extended.
func TestSealer_RefusesToExtendFailedChain(t *testing.T) {
	ring := keyring{}
	failed := "ARC-Seal: i=1; a=rsa-sha256; cv=fail; d=broken.test; s=default; t=1; b=AAAA\r\n" +
		forwardedMessage

	sealed, err := newSealer(t, "forwarder.test", dkim.AlgorithmRSA2048, ring).
		Seal(context.Background(), []byte(failed), passingResult())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if string(sealed) != failed {
		t.Error("a failed chain was extended instead of being left alone")
	}
}

// The relaxed body canonicalization of RFC 6376 section 3.4.4 must ignore
// trailing whitespace and trailing empty lines, so cosmetic changes in transit
// do not break the signature.
func TestBodyHash_IgnoresCosmeticWhitespace(t *testing.T) {
	base := bodyHash("line one\r\nline two\r\n")

	equivalent := []string{
		"line one  \r\nline two\r\n",
		"line one\r\nline two\r\n\r\n\r\n",
		"line one\r\nline  two\r\n",
	}
	for _, variant := range equivalent {
		if bodyHash(variant) != base {
			t.Errorf("body hash changed for a cosmetically equivalent body: %q", variant)
		}
	}

	if bodyHash("line one\r\nline three\r\n") == base {
		t.Error("body hash did not change for genuinely different content")
	}
}

func TestCanonicalizeHeaderRelaxed(t *testing.T) {
	cases := map[string]string{
		"Subject: hello":        "subject:hello",
		"SUBJECT:   hello  ":    "subject:hello",
		"Subject:\thello there": "subject:hello there",
	}
	for input, want := range cases {
		name, value, _ := strings.Cut(input, ":")
		if got := canonicalizeHeaderRelaxed(name, value); got != want {
			t.Errorf("canonicalizeHeaderRelaxed(%q) = %q, want %q", input, got, want)
		}
	}
}
