package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// DeliveredReportStore reaches reports that were delivered before automatic
// ingestion existed and are still filed in a mailbox.
type DeliveredReportStore struct {
	pool *pgxpool.Pool
}

func NewDeliveredReportStore(pool *pgxpool.Pool) *DeliveredReportStore {
	return &DeliveredReportStore{pool: pool}
}

// reportRecipientPattern matches the local parts the report aliases use. It
// mirrors reportLocalParts in the use case; both exist because this one has to
// be expressible in SQL.
const reportRecipientPattern = `^(dmarc|tlsrpt)@`

// ListUningestedReports returns stored messages addressed to a report alias
// that are not already filed in Reports.
//
// Being outside Reports is what "not yet ingested" means here: the delivery
// path files every report it reads into that folder, so anything addressed to
// a report alias and still sitting elsewhere predates ingestion.
func (s *DeliveredReportStore) ListUningestedReports(
	ctx context.Context, limit int,
) ([]port.StoredReportMessage, error) {
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}

	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.blob_id, recipient
		  FROM email_messages e
		  JOIN folders f ON f.id = e.folder_id
		  CROSS JOIN LATERAL unnest(e.recipient_addresses) AS recipient
		 WHERE e.expunged_at IS NULL
		   AND LOWER(f.name) <> LOWER($1)
		   AND recipient ~* $2
		 ORDER BY e.received_at
		 LIMIT $3
	`, reportsFolderName, reportRecipientPattern, limit)
	if err != nil {
		return nil, fmt.Errorf("list delivered reports: %w", err)
	}
	defer rows.Close()

	out := []port.StoredReportMessage{}
	for rows.Next() {
		var msg port.StoredReportMessage
		if err := rows.Scan(&msg.MessageID, &msg.BlobID, &msg.RecipientAddress); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

// reportsFolderName is duplicated from the use case rather than imported: the
// infrastructure layer must not depend on the application layer.
const reportsFolderName = "Reports"

// MoveToReportsFolder files a backfilled message out of the inbox, keeping the
// counters on both folders in step.
//
// The message keeps its identity and its blob; only the folder and the UID
// change, since a UID means nothing outside one folder.
func (s *DeliveredReportStore) MoveToReportsFolder(ctx context.Context, messageID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var mailboxID uuid.UUID
	var sourceFolderID string
	var wasUnread bool
	err = tx.QueryRow(ctx, `
		SELECT e.mailbox_id, e.folder_id::text,
		       NOT EXISTS (SELECT 1 FROM message_flags mf
		                    WHERE mf.message_id = e.id
		                      AND mf.received_at = e.received_at
		                      AND mf.flag = '\Seen')
		  FROM email_messages e
		 WHERE e.id = $1 AND e.expunged_at IS NULL
	`, messageID).Scan(&mailboxID, &sourceFolderID, &wasUnread)
	if err != nil {
		return fmt.Errorf("find message %s: %w", messageID, err)
	}

	var targetFolderID string
	var targetUID int64
	err = tx.QueryRow(ctx, `
		SELECT id::text, uid_next FROM folders
		 WHERE mailbox_id = $1 AND LOWER(name) = LOWER($2)
		 LIMIT 1
		 FOR UPDATE
	`, mailboxID, reportsFolderName).Scan(&targetFolderID, &targetUID)
	if err != nil {
		return fmt.Errorf("find the %s folder for mailbox %s: %w", reportsFolderName, mailboxID, err)
	}
	if targetFolderID == sourceFolderID {
		return nil
	}

	var modseq int64
	if err := tx.QueryRow(ctx, `
		UPDATE folders SET uid_next = uid_next + 1, highest_modseq = highest_modseq + 1,
		       total_count = total_count + 1
		 WHERE id = $1
		 RETURNING highest_modseq
	`, targetFolderID).Scan(&modseq); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE email_messages SET folder_id = $2, uid = $3, modseq = $4 WHERE id = $1
	`, messageID, targetFolderID, targetUID, modseq); err != nil {
		return err
	}

	// A backfilled report is machine-read mail: marking it seen is what stops
	// it raising an unread badge in its new home.
	if _, err := tx.Exec(ctx, `
		INSERT INTO message_flags (message_id, received_at, flag)
		SELECT e.id, e.received_at, '\Seen' FROM email_messages e WHERE e.id = $1
		ON CONFLICT DO NOTHING
	`, messageID); err != nil {
		return err
	}

	unreadDelta := 0
	if wasUnread {
		unreadDelta = -1
	}
	if _, err := tx.Exec(ctx, `
		UPDATE folders
		   SET total_count = GREATEST(0, total_count - 1),
		       unread_count = GREATEST(0, unread_count + $2),
		       highest_modseq = highest_modseq + 1
		 WHERE id = $1
	`, sourceFolderID, unreadDelta); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
