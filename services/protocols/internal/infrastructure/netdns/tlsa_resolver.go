package netdns

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"

	"lambdamail/protocols/internal/domain/valueobject"
)

// TlsaResolver looks up DANE associations through a validating resolver. It
// reports whether the answer carried the AD bit, because a TLSA record that
// was not DNSSEC-validated is worthless: an attacker able to spoof DNS could
// otherwise choose the policy we apply (PLAN.md section 5.1).
type TlsaResolver struct {
	// serverAddr is the validating resolver to ask, as host:port.
	serverAddr string
	client     *dns.Client
}

func NewTlsaResolver(serverAddr string) *TlsaResolver {
	if serverAddr == "" {
		serverAddr = "1.1.1.1:53"
	}
	if !strings.Contains(serverAddr, ":") {
		serverAddr += ":53"
	}
	return &TlsaResolver{
		serverAddr: serverAddr,
		client:     &dns.Client{Timeout: 10 * time.Second},
	}
}

func (r *TlsaResolver) LookupTLSA(ctx context.Context, host string, port int) ([]valueobject.TLSARecord, bool, error) {
	name := dns.Fqdn(fmt.Sprintf("_%d._tcp.%s", port, strings.TrimSuffix(host, ".")))

	msg := new(dns.Msg)
	msg.SetQuestion(name, dns.TypeTLSA)
	// Ask the resolver to validate and to report the result in the AD bit.
	msg.SetEdns0(4096, true)
	msg.AuthenticatedData = true

	reply, _, err := r.client.ExchangeContext(ctx, msg, r.serverAddr)
	if err != nil {
		return nil, false, fmt.Errorf("query TLSA for %s: %w", name, err)
	}

	switch reply.Rcode {
	case dns.RcodeSuccess:
		// Fall through to parsing.
	case dns.RcodeNameError:
		// No TLSA record: the destination is simply not DANE-protected.
		return nil, reply.AuthenticatedData, nil
	default:
		return nil, false, fmt.Errorf("TLSA query for %s returned %s", name, dns.RcodeToString[reply.Rcode])
	}

	var records []valueobject.TLSARecord
	for _, answer := range reply.Answer {
		tlsa, ok := answer.(*dns.TLSA)
		if !ok {
			continue
		}
		// Usage 2 is rejected by explicit decision (PLAN.md section 5.1), so
		// unsupported associations are skipped rather than failing the lookup.
		record, err := valueobject.NewTLSARecord(tlsa.Usage, tlsa.Selector, tlsa.MatchingType, strings.ToLower(tlsa.Certificate))
		if err != nil {
			continue
		}
		records = append(records, record)
	}

	return records, reply.AuthenticatedData, nil
}
