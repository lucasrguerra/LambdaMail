package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// ImapFolderRepository resolves a mailbox's folders for LIST and SELECT. The
// question is answered by MailStore, the same one the webmail asks, so a folder
// reports the same number of messages on both.
type ImapFolderRepository struct {
	store *MailStore
}

func NewImapFolderRepository(pool *pgxpool.Pool) *ImapFolderRepository {
	return &ImapFolderRepository{store: NewMailStore(pool)}
}

func imapRecord(f FolderInfo) port.ImapFolderRecord {
	return port.ImapFolderRecord{
		ID:            f.ID,
		Name:          f.Name,
		UIDNext:       uint32(f.UIDNext),
		UIDValidity:   uint32(f.UIDValidity),
		NumMessages:   uint32(f.Total),
		HighestModSeq: uint64(f.HighestModSeq),
	}
}

func (r *ImapFolderRepository) FindByName(ctx context.Context, mailboxID, name string) (*port.ImapFolderRecord, error) {
	f, err := r.store.FolderByName(ctx, mailboxID, name)
	if err != nil || f == nil {
		return nil, err
	}
	rec := imapRecord(*f)
	return &rec, nil
}

func (r *ImapFolderRepository) ListFolders(ctx context.Context, mailboxID string) ([]port.ImapFolderRecord, error) {
	folders, err := r.store.Folders(ctx, mailboxID)
	if err != nil {
		return nil, err
	}
	var out []port.ImapFolderRecord
	for _, f := range folders {
		out = append(out, imapRecord(f))
	}
	return out, nil
}
