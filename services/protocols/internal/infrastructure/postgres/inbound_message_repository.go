package postgres

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// InboundMessageRepository implements port.InboundMessageRepository against
// Postgres: one transaction per Persist call, matching PLAN.md section 6.1's
// "250 OK only after fsync + COMMIT" rule (the fsync happens in BlobStorage;
// this repository's COMMIT is the second half of that durability guarantee).
type InboundMessageRepository struct {
	pool *pgxpool.Pool
}

func NewInboundMessageRepository(pool *pgxpool.Pool) *InboundMessageRepository {
	return &InboundMessageRepository{pool: pool}
}

// PersistAll records every recipient's copy in one transaction, so a delivery
// that fans out is all-or-nothing and a retried SMTP transaction cannot leave
// an earlier recipient with a duplicate.
func (r *InboundMessageRepository) PersistAll(ctx context.Context, inputs []port.PersistInboundMessageInput) ([]int64, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) // no-op if Commit succeeds

	uids := make([]int64, 0, len(inputs))
	for _, input := range inputs {
		uid, err := persistOne(ctx, tx, input)
		if err != nil {
			return nil, err
		}
		uids = append(uids, uid)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return uids, nil
}

// lockFolder resolves a folder by special-use role or name and locks the row,
// which is what serialises UID allocation.
func lockFolder(ctx context.Context, tx pgx.Tx, mailboxID any, folder string) (string, int64, error) {
	var id string
	var uid int64
	err := tx.QueryRow(ctx, `
		SELECT id, uid_next FROM folders
		WHERE mailbox_id = $1 AND (special_use = LOWER($2) OR LOWER(name) = LOWER($2))
		ORDER BY special_use IS NOT NULL DESC
		LIMIT 1
		FOR UPDATE
	`, mailboxID, folder).Scan(&id, &uid)
	return id, uid, err
}

// nullIfEmpty keeps an absent header as SQL NULL rather than an empty string,
// so "no subject" and "empty subject" stay distinguishable.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// persistOne writes one recipient's copy inside the caller's transaction.
func persistOne(ctx context.Context, tx pgx.Tx, input port.PersistInboundMessageInput) (int64, error) {
	targetFolder := input.TargetFolderName
	if targetFolder == "" {
		targetFolder = "INBOX"
	}

	// The target folder is resolved with INBOX as a fallback. A message the
	// spam filter routes to Junk must not be refused outright because that
	// folder was never created - the delivery is what matters, and refusing it
	// makes the sender retry forever against a mailbox that will never grow a
	// Junk folder on its own.
	folderID, uid, err := lockFolder(ctx, tx, input.MailboxID, targetFolder)
	if errors.Is(err, pgx.ErrNoRows) && !strings.EqualFold(targetFolder, "INBOX") {
		log.Printf("delivery: mailbox %s has no %q folder, filing in INBOX instead", input.MailboxID, targetFolder)
		folderID, uid, err = lockFolder(ctx, tx, input.MailboxID, "INBOX")
	}
	if err != nil {
		return 0, fmt.Errorf("find folder %q for mailbox %s: %w", targetFolder, input.MailboxID, err)
	}

	// A copy the user wrote themselves arrives already read, so it must not
	// raise the unread counter. Incrementing unconditionally is what left the
	// Sent and Drafts folders advertising unread mail forever: nothing ever
	// opens your own outgoing copy, so the counter had no way back down.
	unreadDelta := 1
	if input.AlreadySeen {
		unreadDelta = 0
	}

	var modseq int64
	if err := tx.QueryRow(ctx, `
		UPDATE folders SET uid_next = uid_next + 1, highest_modseq = highest_modseq + 1,
		       unread_count = unread_count + $2, total_count = total_count + 1
		WHERE id = $1
		RETURNING highest_modseq
	`, folderID, unreadDelta).Scan(&modseq); err != nil {
		return 0, fmt.Errorf("advance uid_next, highest_modseq and folder counters: %w", err)
	}

	// email_messages.id defaults to gen_random_uuid() per the migration - no explicit value needed.
	// id and received_at come back because message_flags is keyed on both:
	// email_messages is partitioned by received_at, so the flag rows cannot be
	// written without the same timestamp the row landed on.
	var messageID uuid.UUID
	var receivedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO email_messages (
			mailbox_id, folder_id, uid, modseq, blob_id,
			sender_address, recipient_addresses, size_bytes,
			subject, snippet, from_display_name, message_id_header, has_attachments,
			spf_result, dkim_result, dmarc_result
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id, received_at
	`, input.MailboxID, folderID, uid, modseq, input.Blob.ID,
		input.SenderAddress, []string{input.RecipientAddress}, input.Blob.SizeBytes,
		nullIfEmpty(input.Subject), nullIfEmpty(input.Snippet), nullIfEmpty(input.FromDisplayName),
		nullIfEmpty(input.MessageIDHeader), input.HasAttachments,
		// These three columns are constrained to the RFC verdict vocabulary
		// ('pass', 'fail', 'none', ...) and are nullable, so "not evaluated"
		// has to be NULL. Passing the Go zero value straight through sent an
		// empty string, which satisfies neither the CHECK nor the meaning.
		//
		// It only ever surfaced on messages this server composed itself - the
		// Sent copy and drafts have no authentication results by definition -
		// and it failed the whole insert. Sent stayed empty because that write
		// is deliberately non-fatal, and saving a draft reported an error.
		nullIfEmpty(input.SPFResult), nullIfEmpty(input.DKIMResult), nullIfEmpty(input.DMARCResult)).
		Scan(&messageID, &receivedAt)
	if err != nil {
		return 0, fmt.Errorf("insert email_messages: %w", err)
	}

	// The flags that describe what this copy is. Without \Seen on an outgoing
	// copy the unread counters never settle; without \Draft an IMAP client
	// shows a half-written message as ordinary mail and offers to reply to it.
	for _, flag := range messageFlagsFor(input, targetFolder) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO message_flags (message_id, received_at, flag)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING
		`, messageID, receivedAt, flag); err != nil {
			return 0, fmt.Errorf("set flag %s: %w", flag, err)
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE message_blobs SET ref_count = ref_count + 1 WHERE id = $1`, input.Blob.ID); err != nil {
		return 0, fmt.Errorf("increment blob ref_count: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE mailboxes SET used_bytes = used_bytes + $1 WHERE id = $2`, input.Blob.SizeBytes, input.MailboxID); err != nil {
		return 0, fmt.Errorf("increment mailbox used_bytes: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO domain_events_outbox (event_type, aggregate_id, payload)
		VALUES ('EmailReceived', $1, $2)
	`, input.MailboxID, fmt.Sprintf(`{"recipient":%q,"uid":%d}`, input.RecipientAddress, uid)); err != nil {
		return 0, fmt.Errorf("insert outbox event: %w", err)
	}

	return uid, nil
}

// messageFlagsFor returns the IMAP flags a newly filed copy is born with.
func messageFlagsFor(input port.PersistInboundMessageInput, folder string) []string {
	flags := []string{}
	if input.AlreadySeen {
		flags = append(flags, `\Seen`)
	}
	if strings.EqualFold(folder, "Drafts") {
		flags = append(flags, `\Draft`)
	}
	return flags
}
