package usecase

import (
	"context"
	"fmt"
	"testing"
)

type fakeDkimStore struct {
	// keyed by algorithm
	publicKeys map[string]string
	inserts    int
}

func newFakeDkimStore() *fakeDkimStore {
	return &fakeDkimStore{publicKeys: map[string]string{}}
}

func (f *fakeDkimStore) FindPublicKey(_ context.Context, _, algorithm string) (string, error) {
	return f.publicKeys[algorithm], nil
}

func (f *fakeDkimStore) Insert(_ context.Context, _, _, algorithm string, _ []byte, publicKey string) error {
	f.inserts++
	if _, exists := f.publicKeys[algorithm]; exists {
		return nil // an active key already won the race
	}
	f.publicKeys[algorithm] = publicKey
	return nil
}

func countingGenerator(calls *int) DkimKeyGenerator {
	return func(algorithm string) ([]byte, string, error) {
		*calls++
		return []byte("PEM-" + algorithm), fmt.Sprintf("PUB-%s-%d", algorithm, *calls), nil
	}
}

// PLAN.md section 5 requires both an RSA and an Ed25519 key per domain, and
// their public halves feed DNS records 5 and 6 (section 7.1).
func TestProvisionDkimKeys_CreatesBothAlgorithms(t *testing.T) {
	store := newFakeDkimStore()
	var generated int

	out, err := NewProvisionDkimKeysUseCase(store, countingGenerator(&generated)).
		Execute(context.Background(), "example.test")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if generated != 2 {
		t.Errorf("generated %d keys, want 2", generated)
	}
	if out.RsaPublicKey == "" || out.Ed25519PublicKey == "" {
		t.Errorf("missing public keys for DNS: rsa=%q ed25519=%q", out.RsaPublicKey, out.Ed25519PublicKey)
	}
	if len(out.Created) != 2 {
		t.Errorf("Created = %v, want both algorithms", out.Created)
	}
}

// Re-running onboarding must not mint new keys: rotating the DKIM key on
// every sync would invalidate signatures already in flight.
func TestProvisionDkimKeys_IsIdempotent(t *testing.T) {
	store := newFakeDkimStore()
	var generated int
	uc := NewProvisionDkimKeysUseCase(store, countingGenerator(&generated))
	ctx := context.Background()

	first, err := uc.Execute(ctx, "example.test")
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	second, err := uc.Execute(ctx, "example.test")
	if err != nil {
		t.Fatalf("second Execute: %v", err)
	}

	if generated != 2 {
		t.Errorf("generated %d keys across two runs, want 2", generated)
	}
	if len(second.Created) != 0 {
		t.Errorf("second run reported creating %v, want nothing", second.Created)
	}
	if second.RsaPublicKey != first.RsaPublicKey || second.Ed25519PublicKey != first.Ed25519PublicKey {
		t.Error("public keys changed between runs; DNS would be rewritten every sync")
	}
}

// If another process wins the insert, the DNS record must carry the key that
// actually became active, not the one this process generated and discarded.
func TestProvisionDkimKeys_ReturnsStoredKeyAfterLostRace(t *testing.T) {
	store := newFakeDkimStore()
	store.publicKeys[rsaAlgorithm] = "" // absent on first read

	uc := NewProvisionDkimKeysUseCase(&raceStore{inner: store}, countingGenerator(new(int)))

	out, err := uc.Execute(context.Background(), "example.test")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.RsaPublicKey != "PUB-WINNER" {
		t.Errorf("RsaPublicKey = %q, want the key that won the insert", out.RsaPublicKey)
	}
}

// raceStore reports no key on the first lookup, then a key inserted by
// somebody else.
type raceStore struct {
	inner  *fakeDkimStore
	probed map[string]bool
}

func (r *raceStore) FindPublicKey(ctx context.Context, domain, algorithm string) (string, error) {
	if r.probed == nil {
		r.probed = map[string]bool{}
	}
	if !r.probed[algorithm] {
		r.probed[algorithm] = true
		return "", nil
	}
	return "PUB-WINNER", nil
}

func (r *raceStore) Insert(ctx context.Context, domain, selector, algorithm string, pem []byte, publicKey string) error {
	return nil
}
