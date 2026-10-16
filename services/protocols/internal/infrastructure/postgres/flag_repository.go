package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// FlagRepository implements port.FlagRepository (IMAP STORE). The work is done
// by MailStore, which every path shares; this only names the message the way
// IMAP does, by folder and UID.
type FlagRepository struct {
	store *MailStore
}

func NewFlagRepository(pool *pgxpool.Pool) *FlagRepository {
	return &FlagRepository{store: NewMailStore(pool)}
}

func (r *FlagRepository) SetFlags(ctx context.Context, folderID string, uid uint32, op port.FlagOp, flags []string, unchangedSince uint64) (bool, error) {
	return r.store.SetFlags(ctx, folderID, uid, op, flags, unchangedSince)
}
