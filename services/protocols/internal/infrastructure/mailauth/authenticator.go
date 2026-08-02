// Package mailauth runs the inbound authentication chain of PLAN.md section
// 6.1: SPF over the envelope, DKIM over the message, then DMARC alignment on
// top of both.
package mailauth

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/mail"
	"strings"

	spflib "blitiri.com.ar/go/spf"
	dkimlib "github.com/emersion/go-msgauth/dkim"
	dmarclib "github.com/emersion/go-msgauth/dmarc"

	"lambdamail/protocols/internal/application/port"
)

// LookupTXTFunc resolves TXT records. It is injected so the chain can be
// tested against a fixed zone instead of the live internet.
type LookupTXTFunc func(domain string) ([]string, error)

// Authenticator implements port.MailAuthenticator.
type Authenticator struct {
	// authServID is the name this server puts in the Authentication-Results
	// header so that downstream consumers can tell our verdicts from a
	// forwarder's (RFC 8601 section 2.4).
	authServID string
	lookupTXT  LookupTXTFunc
	resolver   spflib.DNSResolver
}

func NewAuthenticator(authServID string) *Authenticator {
	if authServID == "" {
		authServID = "lambdamail"
	}
	return &Authenticator{authServID: authServID, lookupTXT: net.LookupTXT, resolver: net.DefaultResolver}
}

// WithLookupTXT overrides DNS resolution, for tests and for pointing the
// checks at a validating resolver.
func (a *Authenticator) WithLookupTXT(lookup LookupTXTFunc, resolver spflib.DNSResolver) *Authenticator {
	a.lookupTXT = lookup
	a.resolver = resolver
	return a
}

func (a *Authenticator) Authenticate(ctx context.Context, input port.InboundAuthInput) port.InboundAuthResult {
	result := port.InboundAuthResult{
		SPF:   port.AuthResultNone,
		DKIM:  port.AuthResultNone,
		DMARC: port.AuthResultNone,
	}

	spfResult, spfDomain := a.checkSPF(ctx, input)
	result.SPF = spfResult

	dkimResult, dkimDomains := a.checkDKIM(input.Message)
	result.DKIM = dkimResult

	fromDomain := headerFromDomain(input.Message)
	result.DMARC, result.DmarcPolicy = a.checkDMARC(fromDomain, spfResult, spfDomain, dkimResult, dkimDomains)

	result.AuthenticationResults = a.formatAuthResults(result, spfDomain, dkimDomains, fromDomain)
	return result
}

func (a *Authenticator) checkSPF(ctx context.Context, input port.InboundAuthInput) (result string, domain string) {
	if input.ClientIP == nil {
		return port.AuthResultNone, ""
	}

	// RFC 7208 section 2.4: with an empty envelope sender (a bounce) the
	// identity checked is the HELO name.
	sender := input.EnvelopeFrom
	if sender == "" {
		sender = "postmaster@" + input.HeloDomain
	}
	domain = domainOf(sender)
	if domain == "" {
		return port.AuthResultNone, ""
	}

	options := []spflib.Option{spflib.WithContext(ctx)}
	if a.resolver != nil {
		options = append(options, spflib.WithResolver(a.resolver))
	}

	res, _ := spflib.CheckHostWithSender(input.ClientIP, input.HeloDomain, sender, options...)
	return string(res), domain
}

func (a *Authenticator) checkDKIM(message []byte) (result string, signingDomains []string) {
	verifications, err := dkimlib.VerifyWithOptions(bytes.NewReader(message), &dkimlib.VerifyOptions{
		LookupTXT: a.lookupTXT,
		// A message carrying hundreds of signatures is a resource-exhaustion
		// attempt, not a legitimate mail (PLAN.md section 12.6).
		MaxVerifications: 10,
	})
	if err != nil && len(verifications) == 0 {
		return port.AuthResultPermError, nil
	}
	if len(verifications) == 0 {
		return port.AuthResultNone, nil
	}

	// One passing signature is enough to authenticate the message; the
	// domains of the passing ones are what DMARC aligns against.
	anyPass := false
	for _, v := range verifications {
		if v.Err == nil {
			anyPass = true
			signingDomains = append(signingDomains, v.Domain)
		}
	}
	if anyPass {
		return port.AuthResultPass, signingDomains
	}
	return port.AuthResultFail, nil
}

func (a *Authenticator) checkDMARC(fromDomain, spfResult, spfDomain, dkimResult string, dkimDomains []string) (result string, policy string) {
	if fromDomain == "" {
		return port.AuthResultNone, ""
	}

	record, err := dmarclib.LookupWithOptions(fromDomain, &dmarclib.LookupOptions{LookupTXT: a.lookupTXT})
	if err != nil {
		if dmarclib.IsTempFail(err) {
			return port.AuthResultTempError, ""
		}
		return port.AuthResultNone, ""
	}
	policy = string(record.Policy)

	// RFC 7489 section 6.6.2: DMARC passes when SPF or DKIM passes *and* the
	// authenticated identifier aligns with the From: domain.
	spfAligned := spfResult == port.AuthResultPass &&
		aligned(fromDomain, spfDomain, record.SPFAlignment == dmarclib.AlignmentStrict)

	dkimAligned := false
	if dkimResult == port.AuthResultPass {
		for _, d := range dkimDomains {
			if aligned(fromDomain, d, record.DKIMAlignment == dmarclib.AlignmentStrict) {
				dkimAligned = true
				break
			}
		}
	}

	if spfAligned || dkimAligned {
		return port.AuthResultPass, policy
	}
	return port.AuthResultFail, policy
}

// aligned implements the identifier alignment of RFC 7489 section 3.1: strict
// requires an exact match, relaxed accepts a shared organizational domain.
func aligned(fromDomain, authDomain string, strict bool) bool {
	if authDomain == "" {
		return false
	}
	fromDomain, authDomain = strings.ToLower(fromDomain), strings.ToLower(authDomain)
	if fromDomain == authDomain {
		return true
	}
	if strict {
		return false
	}
	return strings.HasSuffix(fromDomain, "."+authDomain) || strings.HasSuffix(authDomain, "."+fromDomain)
}

// formatAuthResults renders RFC 8601 section 2.2. The header is written by us
// and describes our own checks, so it must always name this server.
func (a *Authenticator) formatAuthResults(result port.InboundAuthResult, spfDomain string, dkimDomains []string, fromDomain string) string {
	var sb strings.Builder
	sb.WriteString(a.authServID)

	fmt.Fprintf(&sb, ";\r\n\tspf=%s", result.SPF)
	if spfDomain != "" {
		fmt.Fprintf(&sb, " smtp.mailfrom=%s", spfDomain)
	}

	fmt.Fprintf(&sb, ";\r\n\tdkim=%s", result.DKIM)
	if len(dkimDomains) > 0 {
		fmt.Fprintf(&sb, " header.d=%s", dkimDomains[0])
	}

	fmt.Fprintf(&sb, ";\r\n\tdmarc=%s", result.DMARC)
	if fromDomain != "" {
		fmt.Fprintf(&sb, " header.from=%s", fromDomain)
	}

	return sb.String()
}

// headerFromDomain extracts the domain of the From: header, which is the
// identity DMARC is defined over (RFC 7489 section 6.6.1).
func headerFromDomain(message []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(message))
	if err != nil {
		return ""
	}
	addresses, err := mail.ParseAddressList(msg.Header.Get("From"))
	if err != nil || len(addresses) == 0 {
		return ""
	}
	return domainOf(addresses[0].Address)
}

func domainOf(address string) string {
	if idx := strings.LastIndex(address, "@"); idx >= 0 && idx < len(address)-1 {
		return strings.ToLower(address[idx+1:])
	}
	return ""
}

// Ensure port interface compliance
var _ port.MailAuthenticator = (*Authenticator)(nil)
