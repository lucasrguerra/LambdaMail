package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// AuthRepository implements port.AuthRepository against Postgres.
type AuthRepository struct {
	pool *pgxpool.Pool
}

func NewAuthRepository(pool *pgxpool.Pool) *AuthRepository {
	return &AuthRepository{pool: pool}
}

func (r *AuthRepository) FindByAddress(ctx context.Context, address string) (*port.MailboxAuth, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT m.id, m.password_hash
		FROM mailboxes m
		JOIN domains d ON d.id = m.domain_id
		WHERE m.email_address = $1
		  AND m.is_active = true
		  AND d.is_active = true
	`, address)

	var rec port.MailboxAuth
	var id uuid.UUID
	if err := row.Scan(&id, &rec.PasswordHash); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.ID = id
	return &rec, nil
}
