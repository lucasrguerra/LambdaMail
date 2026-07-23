// Package postgres contains pgx-based implementations of the application's
// repository ports (PLAN.md section 3's ADR: pgx for Go repositories).
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool builds a connection pool for databaseURL (a postgres:// URL).
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, databaseURL)
}
