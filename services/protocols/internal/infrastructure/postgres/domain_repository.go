package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DomainRepository lists the domains this server is responsible for.
type DomainRepository struct {
	pool *pgxpool.Pool
}

func NewDomainRepository(pool *pgxpool.Pool) *DomainRepository {
	return &DomainRepository{pool: pool}
}

// ActiveDomainNames returns every active domain.
//
// The DNS reconciler used to run against MAIL_DOMAIN alone, so a domain added
// through the console was never reconciled: its records simply never appeared,
// and the only visible symptom was the console reporting them missing forever.
func (r *DomainRepository) ActiveDomainNames(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT name FROM domains WHERE is_active ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list active domains: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// SaveDnsStatus files the outcome of a verification against the domain.
//
// The console reads its badge from this column, and nothing ever wrote to it
// after onboarding: a domain whose records were all published and resolving
// was still listed as PENDING, last checked "never".
func (r *DomainRepository) SaveDnsStatus(ctx context.Context, domain, status string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE domains
		   SET dns_status = $2, dns_last_checked_at = now()
		 WHERE lower(name) = lower($1)
	`, domain, columnStatus(status))
	if err != nil {
		return fmt.Errorf("save dns status for %s: %w", domain, err)
	}
	return nil
}

// columnStatus maps the verifier's vocabulary onto the one the column accepts.
//
// dns_status is constrained to PENDING, VERIFIED, PARTIAL, DRIFT and ERROR,
// while verification also reports MISSING and UNKNOWN. Writing either of those
// violates the check constraint, so the update fails and the badge silently
// stays stale - the exact symptom this was meant to fix.
func columnStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "VERIFIED", "PARTIAL", "DRIFT", "ERROR", "PENDING":
		return strings.ToUpper(strings.TrimSpace(status))
	case "MISSING":
		// Nothing resolved: the zone is not serving what it should.
		return "ERROR"
	default:
		// UNKNOWN, and anything new the verifier learns to say.
		return "PENDING"
	}
}
