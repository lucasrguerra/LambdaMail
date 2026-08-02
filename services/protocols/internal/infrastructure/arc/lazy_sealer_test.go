package arc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"sync"
	"testing"

	"lambdamail/protocols/internal/application/port"
)

// stubKeyRepo serves whatever the test decides is provisioned at that moment.
type stubKeyRepo struct {
	mu   sync.Mutex
	keys []port.DkimSigningKey
	// calls counts lookups, to prove the resolved key is cached.
	calls int
}

func (s *stubKeyRepo) FindActiveKeys(_ context.Context, _ string) ([]port.DkimSigningKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.keys, nil
}

func (s *stubKeyRepo) provision(t *testing.T) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = []port.DkimSigningKey{{
		DomainName:    "example.test",
		Selector:      "lmail",
		Algorithm:     "rsa2048",
		PrivateKeyPEM: pemBytes,
	}}
}

func parseTestKey(pemBytes []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(pemBytes)
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

const testMessage = "From: sender@remote.test\r\n" +
	"To: user@example.test\r\n" +
	"Subject: hello\r\n" +
	"\r\n" +
	"body\r\n"

// The composition root builds the sealer before the DKIM provisioner has run.
// Binding at construction would leave ARC off until the next restart.
func TestLazySealer_StartsSealingOnceAKeyIsProvisioned(t *testing.T) {
	repo := &stubKeyRepo{}
	sealer := NewLazySealer(repo, parseTestKey, "example.test", "mail.example.test")
	ctx := context.Background()
	authResult := port.InboundAuthResult{SPF: port.AuthResultPass}

	// No key yet: the message goes through unsealed rather than failing.
	out, err := sealer.Seal(ctx, []byte(testMessage), authResult)
	if err != nil {
		t.Fatalf("Seal without a key: %v", err)
	}
	if strings.Contains(string(out), "ARC-Seal") {
		t.Fatal("a message was sealed before any key existed")
	}

	repo.provision(t)

	out, err = sealer.Seal(ctx, []byte(testMessage), authResult)
	if err != nil {
		t.Fatalf("Seal after provisioning: %v", err)
	}
	if !strings.Contains(string(out), "ARC-Seal") {
		t.Fatal("sealing never started after the key was provisioned")
	}
}

// Once bound, the key is not looked up again on every message.
func TestLazySealer_CachesTheResolvedKey(t *testing.T) {
	repo := &stubKeyRepo{}
	repo.provision(t)
	sealer := NewLazySealer(repo, parseTestKey, "example.test", "mail.example.test")
	ctx := context.Background()

	for range 3 {
		if _, err := sealer.Seal(ctx, []byte(testMessage), port.InboundAuthResult{}); err != nil {
			t.Fatalf("Seal: %v", err)
		}
	}

	if repo.calls != 1 {
		t.Errorf("FindActiveKeys called %d times, want 1", repo.calls)
	}
}
