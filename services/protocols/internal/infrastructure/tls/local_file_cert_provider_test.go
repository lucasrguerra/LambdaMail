package tlsprovider

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generateSelfSignedPEMForTest returns a minimal self-signed cert/key pair
// generated with Go's crypto/x509, so this test has no external dependency
// on openssl being installed.
func generateSelfSignedPEMForTest(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.local"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// writeTestCertAndKey writes a self-signed cert/key pair to disk for testing
func writeTestCertAndKey(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	certPEM, keyPEM := generateSelfSignedPEMForTest(t)
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

func TestNewLocalFileCertProvider_LoadsValidCertAndKey(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCertAndKey(t, dir)

	provider, err := NewLocalFileCertProvider(certPath, keyPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cert, err := provider.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate error: %v", err)
	}
	if cert == nil {
		t.Fatal("GetCertificate returned nil cert")
	}
}

func TestNewLocalFileCertProvider_FailsFastOnMissingFiles(t *testing.T) {
	_, err := NewLocalFileCertProvider("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err == nil {
		t.Fatal("expected error for nonexistent cert/key paths, got nil")
	}
}

func TestNewEphemeralSelfSignedCertProvider_GeneratesUsableCert(t *testing.T) {
	provider, err := NewEphemeralSelfSignedCertProvider()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cert, err := provider.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate error: %v", err)
	}
	if cert == nil {
		t.Fatal("GetCertificate returned nil cert")
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate DER blob")
	}
}
