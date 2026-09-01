package tlsprovider

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"time"

	"lambdamail/protocols/internal/application/port"
)

// ProviderStatus reports on whatever certificate a provider is actually
// serving, by asking it for one and reading the leaf.
//
// The TLS panel used to be wired only when the certificate came from the
// Traefik acme.json watcher, the single provider that carried status methods
// of its own. Every other mode answered 503 "the TLS panel needs a certificate
// watcher" - including the degraded fallback, where the server serves an
// ephemeral self-signed certificate and the operator most needs to be told.
type ProviderStatus struct {
	provider port.CertProvider
}

func NewProviderStatus(provider port.CertProvider) *ProviderStatus {
	return &ProviderStatus{provider: provider}
}

// leafFor returns the parsed leaf the provider would serve for a host.
func (s *ProviderStatus) leafFor(host string) *x509.Certificate {
	if s == nil || s.provider == nil {
		return nil
	}
	cert, err := s.provider.GetCertificate(&tls.ClientHelloInfo{ServerName: host})
	if err != nil || cert == nil {
		return nil
	}
	if cert.Leaf != nil {
		return cert.Leaf
	}
	if len(cert.Certificate) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil
	}
	return leaf
}

func (s *ProviderStatus) HasCertificateFor(host string) bool {
	return s.leafFor(host) != nil
}

// EarliestExpiry reports the expiry of the certificate served for the host it
// was asked about. A single-certificate provider has only one to report.
func (s *ProviderStatus) EarliestExpiry() (string, time.Time, bool) {
	leaf := s.leafFor("")
	if leaf == nil {
		return "", time.Time{}, false
	}
	host := leaf.Subject.CommonName
	if host == "" && len(leaf.DNSNames) > 0 {
		host = leaf.DNSNames[0]
	}
	return host, leaf.NotAfter, true
}

// LastReload and LastChange are unknown for a provider that loads once. The
// panel already treats a zero time as "not reported" rather than as an error.
func (s *ProviderStatus) LastReload() time.Time { return time.Time{} }
func (s *ProviderStatus) LastChange() time.Time { return time.Time{} }

// UnknownSNICount is a watcher-only measure; nothing here counts SNI misses.
func (s *ProviderStatus) UnknownSNICount() int64 { return 0 }

// CertificateSummary names the issuer and says whether the certificate is
// self-signed, which is the difference between a working mail server and one
// every verifying client refuses.
func (s *ProviderStatus) CertificateSummary(host string) (string, bool, bool) {
	leaf := s.leafFor(host)
	if leaf == nil {
		return "", false, false
	}
	issuer := leaf.Issuer.CommonName
	if issuer == "" {
		issuer = leaf.Issuer.String()
	}
	// Equal subject and issuer is what self-signed means on the wire, and
	// verifying the signature with the certificate's own key confirms it.
	// CheckSignatureFrom is the wrong tool: it also demands the issuer be a
	// CA, which a throwaway leaf never claims to be, so it reported the
	// degraded fallback as properly issued.
	selfSigned := bytes.Equal(leaf.RawSubject, leaf.RawIssuer) &&
		leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil
	return issuer, selfSigned, true
}

// CertificateSpkiDigest is the SHA-256 over the certificate's
// SubjectPublicKeyInfo: the number a TLSA "3 1 1" record has to carry.
//
// Published so an operator can compare what is in DNS against what the server
// is actually serving. When those two disagree, every DANE-validating sender
// refuses the mail, and nothing else on the panel would show why.
func (s *ProviderStatus) CertificateSpkiDigest(host string) (string, bool) {
	leaf := s.leafFor(host)
	if leaf == nil {
		return "", false
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:]), true
}
