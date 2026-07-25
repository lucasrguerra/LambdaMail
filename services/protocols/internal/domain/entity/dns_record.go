package entity

import (
	"fmt"
	"strings"
	"time"
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

// Build13DnsRecordSpecs generates the 13 mandatory DNS record specifications per PLAN.md section 7.
func Build13DnsRecordSpecs(domainName, mailHost, serverIPv4, serverIPv6, rsaDkimPubKey, edDkimPubKey, tlsaHash string, daneEnabled bool) []DnsRecord {
	if mailHost == "" {
		mailHost = fmt.Sprintf("mail.%s", domainName)
	}

	records := []DnsRecord{
		// 1. A record for mail host
		{
			Type:    "A",
			Name:    mailHost,
			Value:   serverIPv4,
			TTL:     1, // Auto
			Proxied: false,
			Comment: "LambdaMail Mail Host A Record",
		},
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
			Value:   fmt.Sprintf("v=spf1 mx a:%s -all", mailHost),
			TTL:     1,
			Comment: "LambdaMail SPF Record",
		},
		// 5. RSA DKIM TXT record
		{
			Type:    "TXT",
			Name:    fmt.Sprintf("default._domainkey.%s", domainName),
			Value:   fmt.Sprintf("v=DKIM1; k=rsa; p=%s", rsaDkimPubKey),
			TTL:     1,
			Comment: "LambdaMail RSA DKIM Key",
		},
		// 6. Ed25519 DKIM TXT record
		{
			Type:    "TXT",
			Name:    fmt.Sprintf("default-ed._domainkey.%s", domainName),
			Value:   fmt.Sprintf("v=DKIM1; k=ed25519; p=%s", edDkimPubKey),
			TTL:     1,
			Comment: "LambdaMail Ed25519 DKIM Key",
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
			Value:   fmt.Sprintf("v=STSv1; id=%d", time.Now().Unix()),
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
	}

	// 2. Conditional AAAA record if IPv6 is supplied
	if serverIPv6 != "" {
		records = append(records, DnsRecord{
			Type:    "AAAA",
			Name:    mailHost,
			Value:   serverIPv6,
			TTL:     1,
			Proxied: false,
			Comment: "LambdaMail Mail Host AAAA Record",
		})
	}

	// 11. Conditional TLSA record if DANE is enabled
	if daneEnabled && tlsaHash != "" {
		records = append(records, DnsRecord{
			Type:    "TLSA",
			Name:    fmt.Sprintf("_25._tcp.%s", mailHost),
			Value:   fmt.Sprintf("3 1 1 %s", tlsaHash),
			TTL:     1,
			Comment: "LambdaMail DANE TLSA Record",
		})
	}

	return records
}

func intPtr(v int) *int {
	return &v
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
	return true
}
