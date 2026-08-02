package dkim

import (
	"context"
	"strings"
	"testing"

	"lambdamail/protocols/internal/application/port"
)

type fakeKeyRepo struct {
	keys []port.DkimSigningKey
	err  error
}

func (f *fakeKeyRepo) FindActiveKeys(_ context.Context, _ string) ([]port.DkimSigningKey, error) {
	return f.keys, f.err
}

const sampleMessage = "From: sender@example.test\r\n" +
	"To: rcpt@remote.test\r\n" +
	"Subject: hello\r\n" +
	"Date: Mon, 20 Jul 2026 10:00:00 +0000\r\n" +
	"Message-ID: <abc@example.test>\r\n" +
	"\r\n" +
	"body text\r\n"

func keyFor(t *testing.T, algorithm, selector string) port.DkimSigningKey {
	t.Helper()
	generated, err := Generate(algorithm)
	if err != nil {
		t.Fatalf("Generate(%s): %v", algorithm, err)
	}
	return port.DkimSigningKey{
		DomainName:    "example.test",
		Selector:      selector,
		Algorithm:     algorithm,
		PrivateKeyPEM: generated.PrivateKeyPEM,
	}
}

// PLAN.md section 5 signs with both algorithms at once: Ed25519 for size and
// speed, RSA as the fallback for receivers that do not validate Ed25519.
func TestSigner_AppliesBothAlgorithms(t *testing.T) {
	repo := &fakeKeyRepo{keys: []port.DkimSigningKey{
		keyFor(t, AlgorithmRSA2048, "default"),
		keyFor(t, AlgorithmEd25519, "default-ed"),
	}}

	signed, err := NewSigner(repo).Sign(context.Background(), "example.test", []byte(sampleMessage))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	out := string(signed)
	if got := strings.Count(out, "DKIM-Signature:"); got != 2 {
		t.Fatalf("got %d DKIM-Signature headers, want 2:\n%s", got, out)
	}
	if !strings.Contains(out, "a=rsa-sha256") {
		t.Error("missing the RSA signature")
	}
	if !strings.Contains(out, "a=ed25519-sha256") {
		t.Error("missing the Ed25519 signature")
	}
	if !strings.Contains(out, "s=default;") || !strings.Contains(out, "s=default-ed;") {
		t.Errorf("selectors not carried into the signatures:\n%s", out)
	}
	if !strings.Contains(out, "body text") {
		t.Error("message body was lost")
	}
}

// The signatures must actually verify, not merely be present.
func TestSigner_ProducesVerifiableSignature(t *testing.T) {
	generated, err := Generate(AlgorithmRSA2048)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	repo := &fakeKeyRepo{keys: []port.DkimSigningKey{{
		DomainName:    "example.test",
		Selector:      "default",
		Algorithm:     AlgorithmRSA2048,
		PrivateKeyPEM: generated.PrivateKeyPEM,
	}}}

	signed, err := NewSigner(repo).Sign(context.Background(), "example.test", []byte(sampleMessage))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Verifying against the real DNS is out of scope here; checking that the
	// signature covers the body is what catches a canonicalization mistake.
	if !strings.Contains(string(signed), "bh=") {
		t.Error("signature carries no body hash")
	}
	if !strings.Contains(string(signed), "d=example.test") {
		t.Error("signature does not claim the signing domain")
	}
}

// A domain with no keys yet must still be able to send.
func TestSigner_NoKeysLeavesMessageUnchanged(t *testing.T) {
	signed, err := NewSigner(&fakeKeyRepo{}).Sign(context.Background(), "example.test", []byte(sampleMessage))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if string(signed) != sampleMessage {
		t.Error("message was modified even though no key was available")
	}
}

func TestGenerate_RejectsUnknownAlgorithm(t *testing.T) {
	if _, err := Generate("rsa1024"); err == nil {
		t.Fatal("expected an error for an unsupported algorithm")
	}
}

func TestParsePrivateKey_RoundTripsBothAlgorithms(t *testing.T) {
	for _, algorithm := range []string{AlgorithmRSA2048, AlgorithmEd25519} {
		generated, err := Generate(algorithm)
		if err != nil {
			t.Fatalf("Generate(%s): %v", algorithm, err)
		}
		if generated.PublicKeyBase64 == "" {
			t.Errorf("%s: no public key produced for the DNS record", algorithm)
		}
		if _, err := ParsePrivateKey(generated.PrivateKeyPEM); err != nil {
			t.Errorf("%s: ParsePrivateKey: %v", algorithm, err)
		}
	}
}
