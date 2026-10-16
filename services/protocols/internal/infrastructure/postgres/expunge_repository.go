package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExpungeRepository implements port.ExpungeRepository (IMAP EXPUNGE). MailStore
// does the work - counters, modseq and the quota all move there, for every path.
type ExpungeRepository struct {
	store *MailStore
}

func NewExpungeRepository(pool *pgxpool.Pool) *ExpungeRepository {
	return &ExpungeRepository{store: NewMailStore(pool)}
}

func (r *ExpungeRepository) Expunge(ctx context.Context, folderID string, uids []uint32) error {
	_, err := r.store.Expunge(ctx, folderID, uids)
	return err
}
