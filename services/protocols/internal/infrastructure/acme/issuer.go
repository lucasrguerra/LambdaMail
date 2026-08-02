// Package acme implements mode B of PLAN.md section 8.3: LambdaMail obtains
// and renews the mail host's certificate itself, instead of reading the one
// Traefik manages.
//
// The reason this mode exists at all is DANE. Traefik cannot be told to reuse
// a key we generated, and DANE requires exactly that: the TLSA association for
// the next certificate has to be published, and allowed to propagate, before
// that certificate is served. Without a pre-generated key there is always a
// window in which the published association does not match the certificate,
// and every validating receiver rejects mail during it (PLAN.md section 5.1).
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"

	"lambdamail/protocols/internal/application/port"
)

// LetsEncryptProduction and LetsEncryptStaging are the two directories worth
// naming. Staging exists because the production rate limits are strict enough
// that a misconfigured deploy loop can lock a domain out for a week.
const (
	LetsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"
	LetsEncryptStaging    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// Issuer obtains certificates over ACME with the DNS-01 challenge.
//
// DNS-01 is used rather than HTTP-01 because the mail host does not
// necessarily serve HTTP, and because it is the only challenge that can issue
// a wildcard.
type Issuer struct {
	store           port.CertificateStore
	email           string
	directoryURL    string
	cloudflareToken string
}

func NewIssuer(store port.CertificateStore, email, directoryURL, cloudflareToken string) *Issuer {
	if directoryURL == "" {
		directoryURL = LetsEncryptProduction
	}
	return &Issuer{
		store:           store,
		email:           email,
		directoryURL:    directoryURL,
		cloudflareToken: cloudflareToken,
	}
}

// acmeUser adapts the stored account key to lego's registration.User.
type acmeUser struct {
	email        string
	key          crypto.PrivateKey
	registration *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// GeneratePrivateKey produces a key for the next certificate. It is exported
// because the rollover generates the key well before issuance, so that its
// TLSA association can be published first.
func GeneratePrivateKey() ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate certificate key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal certificate key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// Obtain issues a certificate for the given domains.
//
// When privateKeyPEM is supplied the CSR is built against that key, which is
// what keeps a pre-published TLSA association valid. When it is nil a fresh
// key is generated, which is correct only for deployments without DANE.
func (i *Issuer) Obtain(ctx context.Context, domains []string, privateKeyPEM []byte) (*port.StoredCertificate, error) {
	if len(domains) == 0 {
		return nil, fmt.Errorf("acme: no domains requested")
	}

	client, err := i.client(ctx)
	if err != nil {
		return nil, err
	}

	request := certificate.ObtainRequest{
		Domains: domains,
		Bundle:  true,
	}

	if privateKeyPEM != nil {
		key, err := parsePrivateKey(privateKeyPEM)
		if err != nil {
			return nil, err
		}
		// This is the whole point of mode B: the CA signs a CSR built from a
		// key we chose, so the association published in advance still matches.
		request.PrivateKey = key
	}

	resource, err := client.Certificate.Obtain(request)
	if err != nil {
		return nil, fmt.Errorf("acme: obtain certificate for %v: %w", domains, err)
	}

	notAfter, err := certificateExpiry(resource.Certificate)
	if err != nil {
		return nil, err
	}

	return &port.StoredCertificate{
		Domain:         domains[0],
		CertificatePEM: resource.Certificate,
		PrivateKeyPEM:  resource.PrivateKey,
		NotAfter:       notAfter,
	}, nil
}

// client builds a lego client, reusing the stored account key so that every
// restart does not register a new account with the CA.
func (i *Issuer) client(ctx context.Context) (*lego.Client, error) {
	accountKeyPEM, err := i.store.LoadAccountKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("acme: load account key: %w", err)
	}

	newAccount := accountKeyPEM == nil
	if newAccount {
		if accountKeyPEM, err = GeneratePrivateKey(); err != nil {
			return nil, err
		}
	}

	accountKey, err := parsePrivateKey(accountKeyPEM)
	if err != nil {
		return nil, err
	}

	user := &acmeUser{email: i.email, key: accountKey}

	config := lego.NewConfig(user)
	config.CADirURL = i.directoryURL
	config.Certificate.KeyType = certcrypto.EC256

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("acme: create client: %w", err)
	}

	provider, err := cloudflare.NewDNSProviderConfig(&cloudflare.Config{
		AuthToken: i.cloudflareToken,
		// The challenge record has to be visible before the CA looks, so the
		// provider polls until it can see it itself.
		PropagationTimeout: 5 * time.Minute,
		PollingInterval:    10 * time.Second,
		TTL:                120,
	})
	if err != nil {
		return nil, fmt.Errorf("acme: create cloudflare dns provider: %w", err)
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return nil, fmt.Errorf("acme: configure dns-01 challenge: %w", err)
	}

	if newAccount {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, fmt.Errorf("acme: register account: %w", err)
		}
		user.registration = reg

		if err := i.store.SaveAccountKey(ctx, accountKeyPEM); err != nil {
			return nil, fmt.Errorf("acme: save account key: %w", err)
		}
	} else {
		reg, err := client.Registration.ResolveAccountByKey()
		if err != nil {
			return nil, fmt.Errorf("acme: resolve existing account: %w", err)
		}
		user.registration = reg
	}

	return client, nil
}

func parsePrivateKey(pemBytes []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("acme: private key is not valid PEM")
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("acme: parse private key: %w", err)
	}
	return key, nil
}

// certificateExpiry reads the leaf's NotAfter, which is what drives renewal.
func certificateExpiry(chainPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(chainPEM)
	if block == nil {
		return time.Time{}, fmt.Errorf("acme: issued certificate is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("acme: parse issued certificate: %w", err)
	}
	return cert.NotAfter, nil
}
