package port

import "context"

// ArcSealer adds an Authenticated Received Chain set to a message
// (RFC 8617, PLAN.md section 5).
//
// It matters on a host that passes mail onward: a forwarder that modifies a
// message breaks its DKIM signature, and the final receiver then sees a DMARC
// failure for mail that authenticated correctly at the first hop. The ARC set
// records this hop's verdict and seals it so the failure can be recognised as
// a forwarding artefact rather than a forgery.
type ArcSealer interface {
	// Seal returns the message with an ARC set prepended. A chain that
	// already failed is returned untouched: RFC 8617 section 5.1.2 forbids
	// extending it.
	Seal(ctx context.Context, message []byte, authResult InboundAuthResult) ([]byte, error)
}
