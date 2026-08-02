package port

import (
	"context"

	"lambdamail/protocols/internal/domain/entity"
)

// DnsVerifier re-reads published records through resolvers outside our
// control. PLAN.md section 7.5 requires this step because the provider API
// confirming a write only proves the API accepted it, not that the world can
// see it.
type DnsVerifier interface {
	// VerifyRecord reports whether the record is visible with the expected
	// value. A lookup failure is reported as not-visible with the reason,
	// never as a hard error, so one unreachable resolver cannot fail a sync.
	VerifyRecord(ctx context.Context, record entity.DnsRecord) (visible bool, detail string)
}
