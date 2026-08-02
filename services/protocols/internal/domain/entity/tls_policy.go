package entity

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"strings"

	"lambdamail/protocols/internal/domain/valueobject"
)

// Transport security modes, in the precedence order of RFC 8461 section 2:
// DANE wins when TLSA records exist, MTA-STS applies otherwise, and a
// destination with neither gets opportunistic TLS (RFC 7435).
const (
	TLSModeDane          = "dane"
	TLSModeMtaSts        = "mta-sts"
	TLSModeOpportunistic = "opportunistic"
	// TLSModeRelay records that the message left through a smarthost, which
	// owns transport security from that point on (PLAN.md section 10.4).
	TLSModeRelay = "relay"
	// TLSModeLocal records a message that never crossed a network: the
	// recipient is a mailbox on this same server, so there was no transport
	// to secure. Distinct from opportunistic, which means a network hop was
	// made without a policy to validate against.
	TLSModeLocal = "local"
)

// TLSPolicy is the effective transport security policy for one outbound
// destination (PLAN.md sections 5 and 6.2).
type TLSPolicy struct {
	daneAvailable   bool
	mtaSTSAvailable bool

	// enforce is true when a validation failure must defer the message
	// instead of falling back to an unvalidated connection. It is false for
	// MTA-STS "testing", whose whole purpose is to report without breaking.
	enforce bool

	// mxPatterns are the "mx:" entries of the MTA-STS policy. A host that
	// matches none of them is not covered by the policy.
	mxPatterns []string

	// tlsaRecords are the DANE-EE associations published for the target host.
	tlsaRecords []valueobject.TLSARecord
}

// NewTLSPolicy builds an opportunistic-or-simple policy. It is kept for
// callers that only need the precedence decision.
func NewTLSPolicy(daneAvailable, mtaSTSAvailable bool) TLSPolicy {
	return TLSPolicy{daneAvailable: daneAvailable, mtaSTSAvailable: mtaSTSAvailable}
}

// NewDanePolicy builds a policy backed by TLSA records. DANE is always
// enforcing: the records are DNSSEC-signed, so a mismatch is an attack or a
// misconfiguration, never an acceptable downgrade (RFC 7672 section 2.2).
func NewDanePolicy(records []valueobject.TLSARecord) TLSPolicy {
	return TLSPolicy{daneAvailable: true, enforce: true, tlsaRecords: records}
}

// NewMtaStsPolicy builds a policy from a fetched MTA-STS policy file.
func NewMtaStsPolicy(mxPatterns []string, enforce bool) TLSPolicy {
	return TLSPolicy{mtaSTSAvailable: true, enforce: enforce, mxPatterns: mxPatterns}
}

// Effective returns "dane", "mta-sts" or "opportunistic" following the
// precedence rule in PLAN.md section 5 / RFC 8461 section 2.
func (p TLSPolicy) Effective() string {
	switch {
	case p.daneAvailable:
		return TLSModeDane
	case p.mtaSTSAvailable:
		return TLSModeMtaSts
	default:
		return TLSModeOpportunistic
	}
}

// RequiresValidation reports whether the connection's certificate has to be
// checked before any mail is handed over.
func (p TLSPolicy) RequiresValidation() bool {
	return p.enforce && (p.daneAvailable || p.mtaSTSAvailable)
}

// CoversHost reports whether a candidate MX is inside the policy's scope. An
// MTA-STS policy that does not list the host means the host must not be used
// under that policy (RFC 8461 section 4.1).
func (p TLSPolicy) CoversHost(host string) bool {
	if !p.mtaSTSAvailable {
		return true
	}
	for _, pattern := range p.mxPatterns {
		if matchMxPattern(pattern, host) {
			return true
		}
	}
	return false
}

// matchMxPattern implements the single-label wildcard of RFC 8461 section 4.1:
// "*.example.com" matches "mx1.example.com" but not "a.mx1.example.com".
func matchMxPattern(pattern, host string) bool {
	pattern = strings.ToLower(strings.TrimSuffix(pattern, "."))
	host = strings.ToLower(strings.TrimSuffix(host, "."))

	if !strings.HasPrefix(pattern, "*.") {
		return pattern == host
	}

	suffix := pattern[1:] // ".example.com"
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	label := strings.TrimSuffix(host, suffix)
	return label != "" && !strings.Contains(label, ".")
}

// MatchesCertificate checks a presented certificate chain against the DANE
// associations. Only usage 3 (DANE-EE) is supported by explicit decision
// (PLAN.md section 5.1), so the end-entity certificate is what gets matched
// and the PKIX chain is irrelevant.
func (p TLSPolicy) MatchesCertificate(chain []*x509.Certificate) bool {
	if len(p.tlsaRecords) == 0 || len(chain) == 0 {
		return false
	}

	leaf := chain[0]
	for _, record := range p.tlsaRecords {
		if digestFor(leaf, record) == strings.ToLower(record.Data()) {
			return true
		}
	}
	return false
}

// digestFor renders the certificate the way a TLSA record's selector and
// matching type describe it (RFC 6698 section 2.1.2).
func digestFor(cert *x509.Certificate, record valueobject.TLSARecord) string {
	var material []byte
	switch record.Selector() {
	case 0: // full certificate
		material = cert.Raw
	case 1: // SubjectPublicKeyInfo
		material = cert.RawSubjectPublicKeyInfo
	default:
		return ""
	}

	switch record.MatchingType() {
	case 0: // exact match on the material itself
		return strings.ToLower(hex.EncodeToString(material))
	case 1: // SHA-256
		sum := sha256.Sum256(material)
		return hex.EncodeToString(sum[:])
	default:
		// SHA-512 (type 2) is deliberately unsupported: the reconciler only
		// ever publishes "3 1 1" records (PLAN.md section 7.1).
		return ""
	}
}
