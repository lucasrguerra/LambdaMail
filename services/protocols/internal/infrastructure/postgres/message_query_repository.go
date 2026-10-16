package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// MessageQueryRepository lists a folder's messages for IMAP FETCH, through the
// same MailStore listing the webmail uses.
type MessageQueryRepository struct {
	store *MailStore
}

func NewMessageQueryRepository(pool *pgxpool.Pool) *MessageQueryRepository {
	return &MessageQueryRepository{store: NewMailStore(pool)}
}

func (r *MessageQueryRepository) ListMessages(ctx context.Context, folderID string) ([]port.MessageRecord, error) {
	messages, err := r.store.Messages(ctx, folderID, MessageQuery{})
	if err != nil {
		return nil, err
	}
	var out []port.MessageRecord
	for _, m := range messages {
		out = append(out, port.MessageRecord{
			UID: m.UID, BlobID: m.BlobID, SizeBytes: m.Size, ReceivedAt: m.ReceivedAt, Flags: m.Flags, ModSeq: m.ModSeq,
		})
	}
	return out, nil
}
