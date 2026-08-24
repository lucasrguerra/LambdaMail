package entity

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

type DnsRecord struct {
	ID       string
	Type     string
	Name     string
	Value    string
	TTL      int
	Priority *int
	Proxied  bool
	Comment  string
}

// DnsRecordSpec describes the zone the reconciler should converge on.
type DnsRecordSpec struct {
	DomainName    string
	MailHost      string
	ServerIPv4    string
	ServerIPv6    string
	RsaDkimPubKey string
	EdDkimPubKey  string
	// TlsaHashes are the DANE associations to publish. Two are normally
	// present during a rollover: the current key and the next one
	// (RFC 7671 section 8.1).
	TlsaHashes  []string
	DaneEnabled bool
	// RelaySpfInclude is the mechanism authorising a smarthost to send for
	// this domain. Without it every message the relay forwards fails SPF
	// (PLAN.md section 10.4).
	RelaySpfInclude string
}

// BuildDnsRecordSpecs generates the DNS records of PLAN.md section 7: the 13
// numbered ones of section 7.1 plus the client-autoconfiguration records of
// section 7.2.
func BuildDnsRecordSpecs(spec DnsRecordSpec) []DnsRecord {
	domainName := spec.DomainName
	mailHost := spec.MailHost
	serverIPv4, serverIPv6 := spec.ServerIPv4, spec.ServerIPv6
	rsaDkimPubKey, edDkimPubKey := spec.RsaDkimPubKey, spec.EdDkimPubKey

	if mailHost == "" {
		mailHost = fmt.Sprintf("mail.%s", domainName)
	}

	// The address records for the mail host belong to whichever zone actually
	// contains that host. A second domain served by the same server points its
	// MX at a host it does not own, and publishing an A for it meant trying to
	// write into somebody else's zone: the provider answered "an identical
	// record already exists" and the console showed errors for a domain that
	// was correctly configured.
	ownsMailHost := hostInsideDomain(mailHost, domainName)

	records := []DnsRecord{
		// 3. MX record
		{
			Type:     "MX",
			Name:     domainName,
			Value:    mailHost,
			Priority: intPtr(10),
			TTL:      1,
			Comment:  "LambdaMail Primary MX Record",
		},
		// 4. SPF TXT record
		{
			Type:    "TXT",
			Name:    domainName,
			Value:   buildSpfRecord(mailHost, spec.RelaySpfInclude),
			TTL:     1,
			Comment: "LambdaMail SPF Record",
		},
		// 7. DMARC TXT record
		{
			Type:    "TXT",
			Name:    fmt.Sprintf("_dmarc.%s", domainName),
			Value:   fmt.Sprintf("v=DMARC1; p=quarantine; rua=mailto:dmarc@%s; ruf=mailto:dmarc@%s; fo=1; adkim=s; aspf=s", domainName, domainName),
			TTL:     1,
			Comment: "LambdaMail DMARC Policy",
		},
		// 8. MTA-STS TXT record
		{
			Type:    "TXT",
			Name:    fmt.Sprintf("_mta-sts.%s", domainName),
			Value:   fmt.Sprintf("v=STSv1; id=%s", mtaStsPolicyID(domainName, mailHost)),
			TTL:     1,
			Comment: "LambdaMail MTA-STS Policy Version",
		},
		// 9. CNAME for mta-sts host (serves policy via HTTPS)
		{
			Type:    "CNAME",
			Name:    fmt.Sprintf("mta-sts.%s", domainName),
			Value:   mailHost,
			TTL:     1,
			Proxied: true,
			Comment: "LambdaMail MTA-STS Endpoint",
		},
		// 10. TLS-RPT TXT record
		{
			Type:    "TXT",
			Name:    fmt.Sprintf("_smtp._tls.%s", domainName),
			Value:   fmt.Sprintf("v=TLSRPTv1; rua=mailto:tlsrpt@%s", domainName),
			TTL:     1,
			Comment: "LambdaMail TLS Reporting Destination",
		},
		// 12. SRV for IMAPS (RFC 6186)
		{
			Type:     "SRV",
			Name:     fmt.Sprintf("_imaps._tcp.%s", domainName),
			Value:    fmt.Sprintf("0 1 993 %s", mailHost),
			TTL:      1,
			Priority: intPtr(0),
			Comment:  "LambdaMail IMAPS Autoconfig",
		},
		// 13. SRV for Submissions
		{
			Type:     "SRV",
			Name:     fmt.Sprintf("_submissions._tcp.%s", domainName),
			Value:    fmt.Sprintf("0 1 465 %s", mailHost),
			TTL:      1,
			Priority: intPtr(0),
			Comment:  "LambdaMail Submissions Autoconfig",
		},
		// The remaining client-autoconfiguration records of PLAN.md section
		// 7.2. They are unnumbered there because they are conveniences, but
		// the HTTPS endpoints of section 7.3 are unreachable without them.
		{
			Type:     "SRV",
			Name:     fmt.Sprintf("_pop3s._tcp.%s", domainName),
			Value:    fmt.Sprintf("0 0 995 %s", mailHost),
			TTL:      1,
			Priority: intPtr(0),
			Comment:  "LambdaMail POP3S Autoconfig",
		},
		{
			Type:    "CNAME",
			Name:    fmt.Sprintf("autoconfig.%s", domainName),
			Value:   mailHost,
			TTL:     1,
			Proxied: true,
			Comment: "LambdaMail Thunderbird Autoconfig Endpoint",
		},
		{
			Type:     "SRV",
			Name:     fmt.Sprintf("_autodiscover._tcp.%s", domainName),
			Value:    fmt.Sprintf("0 0 443 %s", mailHost),
			TTL:      1,
			Priority: intPtr(0),
			Comment:  "LambdaMail Outlook Autodiscover",
		},
	}

	// 5 and 6. DKIM records, published only once there is a key to put in them.
	//
	// "p=" with nothing after it is not an absent key, it is a revoked one
	// (RFC 6376 section 3.6.1): a verifier reading it treats every signature
	// from that selector as broken. These used to be emitted unconditionally,
	// so a deployment that had not provisioned keys yet - a fresh database, or
	// a missing master key - published a revocation for its own domain. That is
	// strictly worse than publishing nothing, and on a domain with a strict
	// DMARC policy it quarantines all of its own outbound mail.
	if rsaDkimPubKey != "" {
		records = append(records, DnsRecord{
			Type:    "TXT",
			Name:    fmt.Sprintf("default._domainkey.%s", domainName),
			Value:   fmt.Sprintf("v=DKIM1; k=rsa; p=%s", rsaDkimPubKey),
			TTL:     1,
			Comment: "LambdaMail RSA DKIM Key",
		})
	}
	if edDkimPubKey != "" {
		records = append(records, DnsRecord{
			Type:    "TXT",
			Name:    fmt.Sprintf("default-ed._domainkey.%s", domainName),
			Value:   fmt.Sprintf("v=DKIM1; k=ed25519; p=%s", edDkimPubKey),
			TTL:     1,
			Comment: "LambdaMail Ed25519 DKIM Key",
		})
	}

	// 1. A record for the mail host, only when this domain contains it.
	if ownsMailHost {
		records = append(records, DnsRecord{
			Type:    "A",
			Name:    mailHost,
			Value:   serverIPv4,
			TTL:     1, // Auto
			Proxied: false,
			Comment: "LambdaMail Mail Host A Record",
		})
	}

	// 2. Conditional AAAA record if IPv6 is supplied
	if ownsMailHost && serverIPv6 != "" {
		records = append(records, DnsRecord{
			Type:    "AAAA",
			Name:    mailHost,
			Value:   serverIPv6,
			TTL:     1,
			Proxied: false,
			Comment: "LambdaMail Mail Host AAAA Record",
		})
	}

	// 11. Conditional TLSA records if DANE is enabled. More than one is normal
	// during a rollover: the current association and the next one are both
	// published so the certificate can change without a gap (RFC 7671 8.1).
	if ownsMailHost && spec.DaneEnabled {
		for _, hash := range spec.TlsaHashes {
			if hash == "" {
				continue
			}
			records = append(records, DnsRecord{
				Type:    "TLSA",
				Name:    fmt.Sprintf("_25._tcp.%s", mailHost),
				Value:   fmt.Sprintf("3 1 1 %s", hash),
				TTL:     1,
				Comment: "LambdaMail DANE TLSA Record",
			})
		}
	}

	return records
}

// buildSpfRecord assembles the sender policy. PLAN.md section 5 uses a hard
// fail, which is only safe once every legitimate sending path is listed - so
// the relay, when there is one, has to be included here.
func buildSpfRecord(mailHost, relayInclude string) string {
	mechanisms := []string{"v=spf1", "mx", "a:" + mailHost}
	if relayInclude != "" {
		mechanisms = append(mechanisms, relayInclude)
	}
	return strings.Join(append(mechanisms, "-all"), " ")
}

func intPtr(v int) *int {
	return &v
}

// mtaStsPolicyID derives the MTA-STS policy id from the policy's own content
// rather than from the wall clock. RFC 8461 section 3.1 requires the id to
// change whenever the served policy changes; PLAN.md section 7.5 requires the
// reconciler to be fully idempotent. A timestamp satisfies the first and
// violates the second - every sync would rewrite the record and force every
// sender to re-fetch. Hashing the inputs that define the policy satisfies both.
func mtaStsPolicyID(domainName, mailHost string) string {
	sum := sha256.Sum256([]byte(domainName + "\x00" + mailHost))
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(sum[:8]))
}

// EqualsNormalized compares two DNS records ignoring subtle formatting whitespace/case differences.
func (r DnsRecord) EqualsNormalized(other DnsRecord) bool {
	if r.Type != other.Type {
		return false
	}
	if !strings.EqualFold(r.Name, other.Name) {
		return false
	}
	if strings.TrimSpace(r.Value) != strings.TrimSpace(other.Value) {
		return false
	}
	if r.Proxied != other.Proxied {
		return false
	}
	// MX and SRV priority drift routes mail to the wrong host, so it counts as
	// a difference even though the record value itself matches.
	if (r.Priority == nil) != (other.Priority == nil) {
		return false
	}
	if r.Priority != nil && *r.Priority != *other.Priority {
		return false
	}
	return true
}

// hostInsideDomain reports whether a host name sits inside a domain's zone,
// and so whether this domain is the one that should publish its addresses.
func hostInsideDomain(host, domain string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if host == "" || domain == "" {
		return false
	}
	// The suffix must fall on a label boundary: "notexample.test" is not
	// inside "example.test".
	return host == domain || strings.HasSuffix(host, "."+domain)
}
