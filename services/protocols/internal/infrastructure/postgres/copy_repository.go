package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// CopyRepository implements port.CopyRepository (IMAP COPY) over MailStore.
type CopyRepository struct {
	store *MailStore
}

func NewCopyRepository(pool *pgxpool.Pool) *CopyRepository {
	return &CopyRepository{store: NewMailStore(pool)}
}

func (r *CopyRepository) CopyMessages(ctx context.Context, sourceFolderID string, uids []uint32, destFolderID string) ([]port.CopiedMessage, error) {
	return r.store.Copy(ctx, sourceFolderID, uids, destFolderID)
}
