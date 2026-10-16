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
		SELECT m.id, m.password_hash, m.email_address, d.name, m.max_recipients_per_hour
		FROM mailboxes m
		JOIN domains d ON d.id = m.domain_id
		WHERE m.email_address = $1
		  AND m.is_active = true
		  AND d.is_active = true
	`, address)

	var rec port.MailboxAuth
	var id uuid.UUID
	if err := row.Scan(&id, &rec.PasswordHash, &rec.EmailAddress, &rec.DomainName, &rec.MaxRecipientsPerHour); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.ID = id

	// The newest few only: each one costs an Argon2 comparison on a failed login,
	// so the number considered is bounded.
	rows, err := r.pool.Query(ctx, `
		SELECT id, password_hash, scopes
		FROM app_passwords
		WHERE mailbox_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, id, port.MaxAppPasswordsChecked)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var app port.AppPasswordAuth
		if err := rows.Scan(&app.ID, &app.Hash, &app.Scopes); err != nil {
			return nil, err
		}
		rec.AppPasswords = append(rec.AppPasswords, app)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &rec, nil
}

// RecordAppPasswordUse stamps the moment an app password last signed a client in.
func (r *AuthRepository) RecordAppPasswordUse(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE app_passwords SET last_used_at = NOW() WHERE id = $1`, id)
	return err
}
