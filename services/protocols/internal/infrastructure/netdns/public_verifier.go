package netdns

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"

	"lambdamail/protocols/internal/domain/entity"
)

// DefaultPublicResolvers are the two independent resolvers PLAN.md section 7.5
// names. Two operators are used deliberately: agreement between them is much
// stronger evidence of real propagation than one authoritative answer.
var DefaultPublicResolvers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// PublicVerifier checks published records through public resolvers.
type PublicVerifier struct {
	resolvers []string
	client    *dns.Client
}

func NewPublicVerifier(resolvers ...string) *PublicVerifier {
	if len(resolvers) == 0 {
		resolvers = DefaultPublicResolvers
	}
	normalized := make([]string, 0, len(resolvers))
	for _, r := range resolvers {
		if !strings.Contains(r, ":") {
			r += ":53"
		}
		normalized = append(normalized, r)
	}
	return &PublicVerifier{
		resolvers: normalized,
		client:    &dns.Client{Timeout: 5 * time.Second},
	}
}

func (v *PublicVerifier) VerifyRecord(ctx context.Context, record entity.DnsRecord) (bool, string) {
	qtype, ok := dns.StringToType[strings.ToUpper(record.Type)]
	if !ok {
		return false, fmt.Sprintf("unsupported record type %q", record.Type)
	}

	// A proxied record resolves to the provider's anycast addresses rather
	// than to the value we asked for, so comparing values would always
	// disagree. Presence is all that can be checked.
	checkValue := !record.Proxied

	var lastDetail string
	for _, resolver := range v.resolvers {
		msg := new(dns.Msg)
		msg.SetQuestion(dns.Fqdn(record.Name), qtype)

		reply, _, err := v.client.ExchangeContext(ctx, msg, resolver)
		if err != nil {
			lastDetail = fmt.Sprintf("%s: %v", resolver, err)
			continue
		}
		if reply.Rcode != dns.RcodeSuccess || len(reply.Answer) == 0 {
			lastDetail = fmt.Sprintf("%s: no answer for %s %s", resolver, record.Type, record.Name)
			continue
		}
		if !checkValue {
			return true, ""
		}
		if answerMatches(reply.Answer, record.Value) {
			return true, ""
		}
		lastDetail = fmt.Sprintf("%s: published value does not match the desired one", resolver)
	}

	return false, lastDetail
}

// answerMatches compares the resolver's answer against the desired value.
// Comparison is on the record's own presentation form with the owner name and
// TTL stripped, which is what makes it work uniformly across record types.
func answerMatches(answers []dns.RR, want string) bool {
	want = normalizeRdata(want)

	for _, rr := range answers {
		// String() renders "<name>\t<ttl>\t<class>\t<type>\t<rdata>"; the
		// rdata is everything after the fourth tab.
		parts := strings.SplitN(rr.String(), "\t", 5)
		if len(parts) < 5 {
			continue
		}
		if normalizeRdata(parts[4]) == want {
			return true
		}
	}
	return false
}

// normalizeRdata removes the differences that do not change meaning: quoting
// of TXT strings, trailing dots on names, case, and repeated whitespace.
func normalizeRdata(value string) string {
	value = strings.ReplaceAll(value, `"`, "")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimSuffix(value, ".")
	return strings.ToLower(value)
}
