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

	// And it is not published as the type it was created as: a proxied CNAME
	// answers A, so asking for the CNAME comes back empty and the record was
	// reported as missing while it was there and serving traffic. For those,
	// the question has to be the type the proxy actually answers.
	qtype = dns.StringToType[queryTypeFor(record.Type, record.Proxied)]

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
		if answerMatches(reply.Answer, record.Type, record.Value) {
			return true, ""
		}
		lastDetail = fmt.Sprintf("%s: published value does not match the desired one", resolver)
	}

	return false, lastDetail
}

// answerMatches compares the resolver's answer against the desired value.
func answerMatches(answers []dns.RR, recordType, want string) bool {
	for _, rr := range answers {
		// String() renders "<name>\t<ttl>\t<class>\t<type>\t<rdata>"; the
		// rdata is everything after the fourth tab.
		parts := strings.SplitN(rr.String(), "\t", 5)
		if len(parts) < 5 {
			continue
		}
		if answerMatchesValue(recordType, parts[4], want) {
			return true
		}
	}
	return false
}

// answerMatchesValue decides whether one published rdata means the same thing
// as the value that was asked for.
//
// The comparison has to be per type. A single normalisation for everything is
// what made the console report records as missing while they were published
// and correct: an MX answer carries a priority the desired value does not, and
// a TXT longer than 255 bytes arrives as several quoted strings that belong
// end to end rather than separated.
func answerMatchesValue(recordType, published, want string) bool {
	switch strings.ToUpper(recordType) {
	case "MX":
		// The spec keeps the priority in its own field, so the desired value
		// is the host alone. Comparing it against "10 mail.example.test."
		// said the MX of a domain receiving mail daily did not exist.
		return normalizeName(mxHost(published)) == normalizeName(want)
	case "TXT", "SPF":
		return joinTxtStrings(published) == joinTxtStrings(want)
	case "CNAME", "NS", "PTR":
		return normalizeName(published) == normalizeName(want)
	default:
		return normalizeName(published) == normalizeName(want)
	}
}

// mxHost drops the preference number an MX answer carries.
func mxHost(rdata string) string {
	fields := strings.Fields(strings.TrimSpace(rdata))
	if len(fields) >= 2 {
		return fields[len(fields)-1]
	}
	return strings.TrimSpace(rdata)
}

// normalizeName removes the differences that do not change what a name means.
func normalizeName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Trim(value, `"`)
	value = strings.TrimSuffix(value, ".")
	return strings.ToLower(strings.TrimSpace(value))
}

// joinTxtStrings puts a TXT value back together.
//
// A value over 255 bytes is published as several strings, and a resolver
// renders them as `"first" "second"`. They are one value end to end: joining
// them with a space inserted one into the middle of a DKIM public key, so
// every RSA DKIM record was reported as missing. Spaces inside a single
// string are meaningful and survive.
func joinTxtStrings(value string) string {
	var out strings.Builder
	inQuote := false
	quoted := false

	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c == '\\' && i+1 < len(value) && inQuote:
			i++
			out.WriteByte(value[i])
		case c == '"':
			inQuote = !inQuote
			quoted = true
		case inQuote:
			out.WriteByte(c)
		case c == ' ' || c == '\t':
			// Whitespace between strings is the rendering, not the value.
			if !quoted {
				out.WriteByte(c)
			}
		default:
			out.WriteByte(c)
		}
	}
	return strings.TrimSpace(out.String())
}

// queryTypeFor is the record type to actually ask a resolver about.
//
// A proxied CNAME is answered as A by the provider, so asking for the CNAME
// returns nothing at all - which is how a record that was published and
// serving traffic came back reported as missing.
func queryTypeFor(recordType string, proxied bool) string {
	upper := strings.ToUpper(recordType)
	if proxied && upper == "CNAME" {
		return "A"
	}
	return upper
}
