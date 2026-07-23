// Package tlsprovider implements port.CertProvider by loading a static
// cert/key pair from disk - suitable for local development. The Traefik
// acme.json directory watcher (PLAN.md section 8) is a separate, later
// adapter behind the same port.CertProvider interface.
package tlsprovider

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"
)

// LocalFileCertProvider serves one certificate loaded once at construction.
type LocalFileCertProvider struct {
	cert tls.Certificate
}

func NewLocalFileCertProvider(certPath, keyPath string) (*LocalFileCertProvider, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load cert/key pair (cert=%s, key=%s): %w", certPath, keyPath, err)
	}
	return &LocalFileCertProvider{cert: cert}, nil
}

func (p *LocalFileCertProvider) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return &p.cert, nil
}

// NewEphemeralSelfSignedCertProvider generates a throwaway self-signed
// certificate in memory. Used as a degraded-mode fallback when no real
// certificate is configured or loadable (PLAN.md section 8.4: never crash
// on a missing cert - serve degraded instead).
func NewEphemeralSelfSignedCertProvider() (*LocalFileCertProvider, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ephemeral key: %w", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ephemeral-self-signed.invalid"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("create ephemeral certificate: %w", err)
	}
	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
	}
	return &LocalFileCertProvider{cert: cert}, nil
}
