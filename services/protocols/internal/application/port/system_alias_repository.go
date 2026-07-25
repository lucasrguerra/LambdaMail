package port

import "context"

// SystemAliasRepository manages mandatory system security aliases (postmaster, abuse, dmarc, tlsrpt).
type SystemAliasRepository interface {
	EnsureSystemAliases(ctx context.Context, domainName string, adminEmail string) error
}
