package port

import (
	"context"

	"lambdamail/protocols/internal/domain/entity"
)

// TLSPolicyResolver determines the transport security policy that applies to
// one outbound destination, combining DANE and MTA-STS with the precedence of
// RFC 8461 section 2 (PLAN.md section 6.2).
type TLSPolicyResolver interface {
	// Resolve never returns an error for a destination that simply publishes
	// no policy: that is an opportunistic destination, not a failure. It
	// errors only when a published policy could not be evaluated, which the
	// caller must treat as a reason to defer rather than to downgrade.
	Resolve(ctx context.Context, destinationDomain string, mxHost string) (entity.TLSPolicy, error)
}
