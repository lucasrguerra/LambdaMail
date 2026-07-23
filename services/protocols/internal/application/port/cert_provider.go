package port

import "crypto/tls"

// CertProvider supplies the TLS certificate for STARTTLS. Its signature
// matches tls.Config.GetCertificate exactly so any implementation can be
// wired in directly: tls.Config{GetCertificate: certProvider.GetCertificate}.
type CertProvider interface {
	GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
}
