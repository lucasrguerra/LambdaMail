package postgres

import (
	"context"
	"fmt"

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
