package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BimiRepository stores the logo a domain publishes for BIMI.
type BimiRepository struct {
	pool *pgxpool.Pool
}

func NewBimiRepository(pool *pgxpool.Pool) *BimiRepository {
	return &BimiRepository{pool: pool}
}

// BimiLogo is a stored mark.
type BimiLogo struct {
	SVG    []byte
	ETag   string
	VmcURL string
}

// Save replaces a domain's logo, returning the digest receivers can cache on.
func (r *BimiRepository) Save(ctx context.Context, domain string, svg []byte, vmcURL string) (string, error) {
	sum := sha256.Sum256(svg)
	etag := hex.EncodeToString(sum[:])

	_, err := r.pool.Exec(ctx, `
		INSERT INTO domain_bimi (domain_id, svg, etag, vmc_url, updated_at)
		SELECT d.id, $2, $3, NULLIF($4, ''), NOW() FROM domains d WHERE lower(d.name) = lower($1)
		ON CONFLICT (domain_id) DO UPDATE
		   SET svg = EXCLUDED.svg, etag = EXCLUDED.etag,
		       vmc_url = EXCLUDED.vmc_url, updated_at = NOW()
	`, domain, svg, etag, vmcURL)
	if err != nil {
		return "", fmt.Errorf("save BIMI logo for %s: %w", domain, err)
	}
	return etag, nil
}

// Load returns a domain's logo, or nil when it has none.
func (r *BimiRepository) Load(ctx context.Context, domain string) (*BimiLogo, error) {
	var logo BimiLogo
	var vmc *string
	err := r.pool.QueryRow(ctx, `
		SELECT b.svg, b.etag, b.vmc_url
		  FROM domain_bimi b JOIN domains d ON d.id = b.domain_id
		 WHERE lower(d.name) = lower($1)
	`, domain).Scan(&logo.SVG, &logo.ETag, &vmc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load BIMI logo for %s: %w", domain, err)
	}
	if vmc != nil {
		logo.VmcURL = *vmc
	}
	return &logo, nil
}

// Delete removes a domain's logo.
func (r *BimiRepository) Delete(ctx context.Context, domain string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM domain_bimi b USING domains d
		 WHERE b.domain_id = d.id AND lower(d.name) = lower($1)
	`, domain)
	if err != nil {
		return fmt.Errorf("delete BIMI logo for %s: %w", domain, err)
	}
	return nil
}
