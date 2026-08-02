package port

import (
	"context"
	"net"
)

// Authentication result values as they appear in an Authentication-Results
// header (RFC 8601 section 2.7) and in the email_messages columns.
const (
	AuthResultNone      = "none"
	AuthResultPass      = "pass"
	AuthResultFail      = "fail"
	AuthResultSoftFail  = "softfail"
	AuthResultNeutral   = "neutral"
	AuthResultTempError = "temperror"
	AuthResultPermError = "permerror"
)

// InboundAuthInput is everything the authentication checks need about one
// inbound transaction.
type InboundAuthInput struct {
	ClientIP     net.IP
	HeloDomain   string
	EnvelopeFrom string
	Message      []byte
}

// InboundAuthResult carries the verdicts recorded against the message and
// surfaced to the user (PLAN.md section 6.1).
type InboundAuthResult struct {
	SPF   string
	DKIM  string
	DMARC string

	// DmarcPolicy is the policy the sending domain published: "none",
	// "quarantine" or "reject". Empty when the domain publishes no record.
	DmarcPolicy string

	// AuthenticationResults is the RFC 8601 header to prepend to the stored
	// message so downstream filters and the user can see how it was judged.
	AuthenticationResults string
}

// MailAuthenticator runs SPF, DKIM and DMARC over an inbound message.
type MailAuthenticator interface {
	// Authenticate never fails the delivery on its own: a DNS outage yields
	// a temperror verdict, not an error.
	Authenticate(ctx context.Context, input InboundAuthInput) InboundAuthResult
}
