package usecase

import (
	"context"
	"fmt"
)

// DkimKeyProvisioner is the storage side of key provisioning. It is declared
// here, next to its consumer, rather than in the shared port package because
// only this use case needs the write path.
type DkimKeyProvisioner interface {
	FindPublicKey(ctx context.Context, domainName, algorithm string) (string, error)
	Insert(ctx context.Context, domainName, selector, algorithm string, privateKeyPEM []byte, publicKey string) error
}

// DkimKeyGenerator produces a fresh key pair for an algorithm.
type DkimKeyGenerator func(algorithm string) (privateKeyPEM []byte, publicKeyBase64 string, err error)

// Selectors match the names published by the DNS reconciler
// (PLAN.md section 7.1, records 5 and 6).
const (
	RsaDkimSelector     = "default"
	Ed25519DkimSelector = "default-ed"

	rsaAlgorithm     = "rsa2048"
	ed25519Algorithm = "ed25519"
)

// ProvisionDkimKeysOutput carries the published halves, which the DNS
// reconciler needs to build the TXT records.
type ProvisionDkimKeysOutput struct {
	RsaPublicKey     string
	Ed25519PublicKey string
	Created          []string
}

// ProvisionDkimKeysUseCase makes sure a domain has one active key per
// algorithm before it is asked to sign anything. It is idempotent: running it
// on a domain that already has keys returns the existing public halves and
// creates nothing.
type ProvisionDkimKeysUseCase struct {
	keys     DkimKeyProvisioner
	generate DkimKeyGenerator
}

func NewProvisionDkimKeysUseCase(keys DkimKeyProvisioner, generate DkimKeyGenerator) *ProvisionDkimKeysUseCase {
	return &ProvisionDkimKeysUseCase{keys: keys, generate: generate}
}

func (uc *ProvisionDkimKeysUseCase) Execute(ctx context.Context, domainName string) (*ProvisionDkimKeysOutput, error) {
	if domainName == "" {
		return nil, fmt.Errorf("provision dkim keys: domain name is required")
	}

	out := &ProvisionDkimKeysOutput{}

	rsaKey, err := uc.ensure(ctx, domainName, rsaAlgorithm, RsaDkimSelector, out)
	if err != nil {
		return nil, err
	}
	out.RsaPublicKey = rsaKey

	edKey, err := uc.ensure(ctx, domainName, ed25519Algorithm, Ed25519DkimSelector, out)
	if err != nil {
		return nil, err
	}
	out.Ed25519PublicKey = edKey

	return out, nil
}

func (uc *ProvisionDkimKeysUseCase) ensure(ctx context.Context, domainName, algorithm, selector string, out *ProvisionDkimKeysOutput) (string, error) {
	existing, err := uc.keys.FindPublicKey(ctx, domainName, algorithm)
	if err != nil {
		return "", fmt.Errorf("look up %s key for %s: %w", algorithm, domainName, err)
	}
	if existing != "" {
		return existing, nil
	}

	privateKeyPEM, publicKey, err := uc.generate(algorithm)
	if err != nil {
		return "", fmt.Errorf("generate %s key for %s: %w", algorithm, domainName, err)
	}

	if err := uc.keys.Insert(ctx, domainName, selector, algorithm, privateKeyPEM, publicKey); err != nil {
		return "", fmt.Errorf("store %s key for %s: %w", algorithm, domainName, err)
	}

	// Re-read rather than trusting the generated value: a concurrent
	// provisioner may have won the insert, and the DNS record has to carry
	// whichever key actually became active.
	stored, err := uc.keys.FindPublicKey(ctx, domainName, algorithm)
	if err != nil {
		return "", fmt.Errorf("confirm %s key for %s: %w", algorithm, domainName, err)
	}

	out.Created = append(out.Created, algorithm)
	return stored, nil
}
