package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AliasRepository struct {
	pool *pgxpool.Pool
}

func NewAliasRepository(pool *pgxpool.Pool) *AliasRepository {
	return &AliasRepository{pool: pool}
}

func (r *AliasRepository) EnsureSystemAliases(ctx context.Context, domainName, adminEmail string) error {
	var domainID uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id FROM domains WHERE name = $1`, domainName).Scan(&domainID)
	if err != nil {
		return fmt.Errorf("find domain %s: %w", domainName, err)
	}

	systemAliasPrefixes := []string{"postmaster", "abuse", "dmarc", "tlsrpt"}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, prefix := range systemAliasPrefixes {
		sourceAddr := fmt.Sprintf("%s@%s", prefix, domainName)
		destArray := []string{adminEmail}

		_, err := tx.Exec(ctx, `
			INSERT INTO aliases (domain_id, source_address, destination_addresses, is_active, is_system)
			VALUES ($1, $2, $3, true, true)
			ON CONFLICT (domain_id, source_address)
			DO UPDATE SET destination_addresses = EXCLUDED.destination_addresses, is_system = true
		`, domainID, sourceAddr, destArray)
		if err != nil {
			return fmt.Errorf("upsert system alias %s: %w", sourceAddr, err)
		}
	}

	return tx.Commit(ctx)
}
