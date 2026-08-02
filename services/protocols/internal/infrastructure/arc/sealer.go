package arc

import (
	"context"
	"crypto"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lambdamail/protocols/internal/application/port"
)

// maxChainInstance caps the chain length. RFC 8617 section 5.1.1 fixes the
// maximum instance number at 50; beyond it the chain must be treated as
// failed rather than extended.
const maxChainInstance = 50

// signedHeaders are the fields the ARC-Message-Signature covers. It mirrors
// the DKIM set: headers a forwarder is likely to add later stay out, or the
// signature would break for the very case ARC exists to handle.
var signedHeaders = []string{
	"from", "to", "cc", "subject", "date", "message-id",
	"mime-version", "content-type", "content-transfer-encoding",
	"in-reply-to", "references",
}

// ChainValidation is the "cv=" tag: the state of the chain as it arrived.
type ChainValidation string

const (
	// ChainNone means no ARC chain was present, so this hop starts one.
	ChainNone ChainValidation = "none"
	// ChainPass means the chain arrived intact.
	ChainPass ChainValidation = "pass"
	// ChainFail means the chain is broken. RFC 8617 section 5.1.2 forbids
	// extending a failed chain, so sealing stops here.
	ChainFail ChainValidation = "fail"
)

// Sealer adds an ARC set to a message being forwarded.
type Sealer struct {
	// domain and selector identify the key, and are the same identity DKIM
	// signs with: an ARC seal is verified through the same DNS record.
	domain   string
	selector string
	signer   crypto.Signer
	// authServID names this server in the ARC-Authentication-Results header.
	authServID string
	now        func() time.Time
}

func NewSealer(domain, selector, authServID string, signer crypto.Signer) *Sealer {
	if authServID == "" {
		authServID = domain
	}
	return &Sealer{
		domain:     domain,
		selector:   selector,
		signer:     signer,
		authServID: authServID,
		now:        time.Now,
	}
}

// WithClock fixes the timestamp, so a test can assert on exact output.
func (s *Sealer) WithClock(now func() time.Time) *Sealer {
	s.now = now
	return s
}

// Seal prepends one ARC set - ARC-Authentication-Results, then
// ARC-Message-Signature, then ARC-Seal - to the message.
//
// The order matters and is prescribed by RFC 8617 section 5.1.1: the seal is
// computed last, over the other two, and is placed above them.
func (s *Sealer) Seal(_ context.Context, message []byte, authResult port.InboundAuthResult) ([]byte, error) {
	text := string(message)
	headers, body := splitMessage(text)

	instance, chainState := s.inspectChain(headers)
	if chainState == ChainFail {
		// Extending a failed chain is forbidden; the message is forwarded as
		// it is so the receiver can see the break for itself.
		return message, nil
	}
	if instance > maxChainInstance {
		return message, nil
	}

	timestamp := s.now().Unix()

	// 1. ARC-Authentication-Results records what this hop concluded.
	aar := fmt.Sprintf("i=%d; %s", instance, s.formatAuthResults(authResult))

	// 2. ARC-Message-Signature covers the message, exactly as a DKIM
	//    signature would, but never covers the ARC-Seal headers.
	ams, err := s.buildMessageSignature(instance, timestamp, headers, body)
	if err != nil {
		return nil, err
	}

	// 3. ARC-Seal covers the whole chain including the two headers above.
	seal, err := s.buildSeal(instance, timestamp, chainState, headers, aar, ams)
	if err != nil {
		return nil, err
	}

	var out strings.Builder
	out.WriteString(HeaderSeal + ": " + seal + "\r\n")
	out.WriteString(HeaderMessageSignature + ": " + ams + "\r\n")
	out.WriteString(HeaderAuthenticationResults + ": " + aar + "\r\n")
	out.WriteString(text)

	return []byte(out.String()), nil
}

// inspectChain finds the instance number this hop should use and the state of
// the chain that arrived.
func (s *Sealer) inspectChain(headers []header) (instance int, state ChainValidation) {
	highest := 0
	previousCV := ""

	for _, h := range headers {
		if !strings.EqualFold(h.name, HeaderSeal) {
			continue
		}
		tags := parseTags(h.value)
		if i, err := strconv.Atoi(tags["i"]); err == nil && i > highest {
			highest = i
			previousCV = tags["cv"]
		}
	}

	if highest == 0 {
		// No chain yet: this hop is instance 1 and reports "none", which is
		// what RFC 8617 section 5.1.1 requires of the first sealer.
		return 1, ChainNone
	}
	if previousCV == string(ChainFail) {
		return highest + 1, ChainFail
	}
	return highest + 1, ChainPass
}

// buildMessageSignature produces the AMS value. It is a DKIM signature in all
// but name, so the tag set and canonicalization match RFC 6376.
func (s *Sealer) buildMessageSignature(instance int, timestamp int64, headers []header, body string) (string, error) {
	present := signedHeaderList(headers)

	unsigned := fmt.Sprintf(
		"i=%d; a=%s; c=relaxed/relaxed; d=%s; s=%s; t=%d; h=%s; bh=%s; b=",
		instance, algorithmFor(s.signer), s.domain, s.selector, timestamp,
		strings.Join(present, ":"), bodyHash(body),
	)

	material := canonicalHeaderBlock(headers, present)
	// The signature covers its own header with an empty "b=" tag
	// (RFC 6376 section 3.7).
	material += canonicalizeHeaderRelaxed(HeaderMessageSignature, unsigned)

	signature, err := sign(s.signer, []byte(material))
	if err != nil {
		return "", err
	}
	return unsigned + signature, nil
}

// buildSeal produces the AS value. Unlike the message signature it covers no
// body and no message headers: only the ARC set headers, in instance order
// (RFC 8617 section 5.1.1).
func (s *Sealer) buildSeal(instance int, timestamp int64, state ChainValidation, headers []header, aar, ams string) (string, error) {
	unsigned := fmt.Sprintf(
		"i=%d; a=%s; cv=%s; d=%s; s=%s; t=%d; b=",
		instance, algorithmFor(s.signer), state, s.domain, s.selector, timestamp,
	)

	var material strings.Builder
	// Every previous ARC set, oldest first.
	for i := 1; i < instance; i++ {
		for _, name := range []string{HeaderAuthenticationResults, HeaderMessageSignature, HeaderSeal} {
			if value, ok := findInstanceHeader(headers, name, i); ok {
				material.WriteString(canonicalizeHeaderRelaxed(name, value))
			}
		}
	}
	// Then this hop's own set, with the seal itself carrying an empty "b=".
	material.WriteString(canonicalizeHeaderRelaxed(HeaderAuthenticationResults, aar))
	material.WriteString(canonicalizeHeaderRelaxed(HeaderMessageSignature, ams))
	material.WriteString(canonicalizeHeaderRelaxed(HeaderSeal, unsigned))

	signature, err := sign(s.signer, []byte(material.String()))
	if err != nil {
		return "", err
	}
	return unsigned + signature, nil
}

// formatAuthResults renders this hop's verdicts for the AAR header.
func (s *Sealer) formatAuthResults(result port.InboundAuthResult) string {
	if result.AuthenticationResults != "" {
		return result.AuthenticationResults
	}
	return fmt.Sprintf("%s; spf=%s; dkim=%s; dmarc=%s",
		s.authServID, orNone(result.SPF), orNone(result.DKIM), orNone(result.DMARC))
}

func orNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// signedHeaderList keeps only the headers actually present, in the order the
// "h=" tag will list them.
func signedHeaderList(headers []header) []string {
	var present []string
	for _, candidate := range signedHeaders {
		for _, h := range headers {
			if strings.EqualFold(h.name, candidate) {
				present = append(present, candidate)
				break
			}
		}
	}
	return present
}

// canonicalHeaderBlock renders the signed headers in "h=" order.
func canonicalHeaderBlock(headers []header, names []string) string {
	var sb strings.Builder
	for _, name := range names {
		for _, h := range headers {
			if strings.EqualFold(h.name, name) {
				sb.WriteString(canonicalizeHeaderRelaxed(h.name, h.value))
				break
			}
		}
	}
	return sb.String()
}

// findInstanceHeader locates the ARC header of a given name and instance.
func findInstanceHeader(headers []header, name string, instance int) (string, bool) {
	for _, h := range headers {
		if !strings.EqualFold(h.name, name) {
			continue
		}
		if parseTags(h.value)["i"] == strconv.Itoa(instance) {
			return h.value, true
		}
	}
	return "", false
}

// parseTags reads the "tag=value; tag=value" form ARC and DKIM headers use.
func parseTags(value string) map[string]string {
	tags := map[string]string{}
	for _, part := range strings.Split(value, ";") {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		tags[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(val)
	}
	return tags
}
