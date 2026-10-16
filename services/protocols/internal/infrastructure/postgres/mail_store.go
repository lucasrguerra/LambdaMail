package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// MailStore is the one place that changes the state of messages and folders.
//
// IMAP, POP3, the webmail and the server's own filing all end up changing the
// same rows, and those rows carry cached numbers that have to stay true: the
// folder's unread and total counters, its highest modseq (what a CONDSTORE
// client uses to learn that something changed), the next UID, and the
// mailbox's used bytes. When each path implemented this on its own they
// drifted apart - reading over IMAP never moved the unread counter, deleting in
// the webmail never gave the quota back, a repeated expunge could push a
// counter below zero - and every fix had to be made twice, which is how one of
// the two went missing.
//
// So each state change is written here, once, in one transaction, and every
// path calls it. The repositories IMAP and the webmail used are now thin
// adapters over this: they translate their way of naming things (a folder id
// and a UID, or a mailbox, a folder name and a UID) and nothing else.
//
// Rules held for every method:
//
//   - A counter never goes below zero, so a double operation cannot corrupt it.
//   - An operation that changes what a client can observe raises the folder's
//     modseq; one that changes nothing does not.
//   - A message is located by folder, so another mailbox's mail cannot be
//     reached by guessing a UID.
type MailStore struct {
	pool *pgxpool.Pool
}

func NewMailStore(pool *pgxpool.Pool) *MailStore {
	return &MailStore{pool: pool}
}

// lockFolders takes the row lock of each folder, in a fixed order.
//
// Every change updates counters on a folder row, and a move updates two. If one
// operation locked the destination folder and then the source, and another did
// the reverse, two moves in opposite directions deadlocked, and so did a move
// racing a flag change (it held the message and wanted the folder, the move held
// the folder and wanted the message). So the order is the same everywhere:
// folders first, lowest id first, then the message.
func lockFolders(ctx context.Context, tx pgx.Tx, ids ...string) error {
	sort.Strings(ids)
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			continue
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM folders WHERE id = $1 FOR UPDATE`, id); err != nil {
			return fmt.Errorf("lock folder %s: %w", id, err)
		}
	}
	return nil
}

// messageRow is the slice of a message every operation needs.
type messageRow struct {
	ID         uuid.UUID
	ReceivedAt any
	MailboxID  string
	Size       int64
	ModSeq     uint64
	Seen       bool
}

// loadMessage finds a live message by folder and UID and locks it, so two
// concurrent changes to the same message cannot both read the old state.
func loadMessage(ctx context.Context, tx pgx.Tx, folderID string, uid uint32) (*messageRow, error) {
	var m messageRow
	err := tx.QueryRow(ctx, `
		SELECT e.id, e.received_at, e.mailbox_id::text, e.size_bytes, e.modseq,
		       EXISTS (SELECT 1 FROM message_flags mf
		                WHERE mf.message_id = e.id AND mf.received_at = e.received_at AND mf.flag = '\Seen')
		  FROM email_messages e
		 WHERE e.folder_id = $1 AND e.uid = $2 AND e.expunged_at IS NULL
		   FOR UPDATE
	`, folderID, int64(uid)).Scan(&m.ID, &m.ReceivedAt, &m.MailboxID, &m.Size, &m.ModSeq, &m.Seen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMessageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find message folder=%s uid=%d: %w", folderID, uid, err)
	}
	return &m, nil
}

// ResolveFolder finds a folder of a mailbox by its special-use role or by its
// name, the way the webmail and IMAP clients both address one ("trash" and
// "Trash" reach the same place; a folder the user made has no role and is
// reached by name). port.ErrFolderMissing when there is none.
func (s *MailStore) ResolveFolder(ctx context.Context, mailboxID, ref string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM folders
		 WHERE mailbox_id = $1 AND (special_use = LOWER($2) OR LOWER(name) = LOWER($2))
		 ORDER BY special_use IS NOT NULL DESC
		 LIMIT 1
	`, mailboxID, ref).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", port.ErrFolderMissing
	}
	return id, err
}

// SetFlags applies op to one message's flags. Add and Del touch individual
// flags, Set replaces the whole set. If unchangedSince > 0 and the message has
// changed since, nothing is written and updated is false (RFC 7162
// UNCHANGEDSINCE).
//
// \Seen is the flag with a side effect: the folder's unread counter follows it,
// in the same transaction, by looking at the flag before and after rather than
// at which operation was asked for. That is what keeps STORE +FLAGS, STORE
// -FLAGS, STORE FLAGS (replace) and the webmail's mark-as-read all counting the
// same way.
func (s *MailStore) SetFlags(ctx context.Context, folderID string, uid uint32, op port.FlagOp, flags []string, unchangedSince uint64) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockFolders(ctx, tx, folderID); err != nil {
		return false, err
	}
	msg, err := loadMessage(ctx, tx, folderID, uid)
	if err != nil {
		return false, err
	}

	if unchangedSince > 0 && msg.ModSeq > unchangedSince {
		return false, nil
	}

	before, err := flagSet(ctx, tx, msg)
	if err != nil {
		return false, err
	}

	switch op {
	case port.FlagOpAdd:
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				INSERT INTO message_flags (message_id, received_at, flag) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, msg.ID, msg.ReceivedAt, flag); err != nil {
				return false, fmt.Errorf("add flag %q: %w", flag, err)
			}
		}
	case port.FlagOpDel:
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				DELETE FROM message_flags WHERE message_id = $1 AND received_at = $2 AND flag = $3`,
				msg.ID, msg.ReceivedAt, flag); err != nil {
				return false, fmt.Errorf("delete flag %q: %w", flag, err)
			}
		}
	case port.FlagOpSet:
		if _, err := tx.Exec(ctx, `
			DELETE FROM message_flags WHERE message_id = $1 AND received_at = $2`, msg.ID, msg.ReceivedAt); err != nil {
			return false, fmt.Errorf("clear flags before set: %w", err)
		}
		for _, flag := range flags {
			if _, err := tx.Exec(ctx, `
				INSERT INTO message_flags (message_id, received_at, flag) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, msg.ID, msg.ReceivedAt, flag); err != nil {
				return false, fmt.Errorf("set flag %q: %w", flag, err)
			}
		}
	}

	after, err := flagSet(ctx, tx, msg)
	if err != nil {
		return false, err
	}

	// Nothing changed: no modseq, no counter. A client that stores a flag the
	// message already has must not be told the folder changed.
	if sameFlags(before, after) {
		return true, tx.Commit(ctx)
	}

	unreadDelta := 0
	switch {
	case before[`\Seen`] && !after[`\Seen`]:
		unreadDelta = 1
	case !before[`\Seen`] && after[`\Seen`]:
		unreadDelta = -1
	}

	var modseq int64
	if err := tx.QueryRow(ctx, `
		UPDATE folders
		   SET highest_modseq = highest_modseq + 1,
		       unread_count = GREATEST(0, unread_count + $2)
		 WHERE id = $1
		RETURNING highest_modseq`, folderID, unreadDelta).Scan(&modseq); err != nil {
		return false, fmt.Errorf("advance folder modseq: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE email_messages SET modseq = $3 WHERE id = $1 AND received_at = $2`,
		msg.ID, msg.ReceivedAt, modseq); err != nil {
		return false, fmt.Errorf("update message modseq: %w", err)
	}

	return true, tx.Commit(ctx)
}

func flagSet(ctx context.Context, tx pgx.Tx, msg *messageRow) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT flag FROM message_flags WHERE message_id = $1 AND received_at = $2`, msg.ID, msg.ReceivedAt)
	if err != nil {
		return nil, fmt.Errorf("read flags: %w", err)
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		set[f] = true
	}
	return set, rows.Err()
}

func sameFlags(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// Expunge removes messages from a folder (soft delete: the row stays, marked,
// and the blob stays referenced until the retention sweep, so a mistake is
// recoverable and a client holding the UID still resolves it).
//
// For each message that was really there it lowers the folder's total, lowers
// unread if the message was unread, and gives its bytes back to the mailbox's
// quota. A message that is already gone is skipped, touching nothing, which is
// what makes a repeated expunge safe. It returns how many messages were
// removed.
func (s *MailStore) Expunge(ctx context.Context, folderID string, uids []uint32) (int, error) {
	if len(uids) == 0 {
		return 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockFolders(ctx, tx, folderID); err != nil {
		return 0, err
	}

	removed := 0
	for _, uid := range uids {
		msg, err := loadMessage(ctx, tx, folderID, uid)
		if errors.Is(err, ErrMessageNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}

		if _, err := tx.Exec(ctx, `UPDATE email_messages SET expunged_at = NOW() WHERE id = $1 AND received_at = $2`,
			msg.ID, msg.ReceivedAt); err != nil {
			return 0, fmt.Errorf("expunge uid %d: %w", uid, err)
		}

		unreadDelta := 0
		if !msg.Seen {
			unreadDelta = -1
		}
		if _, err := tx.Exec(ctx, `
			UPDATE folders
			   SET total_count = GREATEST(0, total_count - 1),
			       unread_count = GREATEST(0, unread_count + $2),
			       highest_modseq = highest_modseq + 1
			 WHERE id = $1`, folderID, unreadDelta); err != nil {
			return 0, fmt.Errorf("lower folder counters for uid %d: %w", uid, err)
		}

		if _, err := tx.Exec(ctx, `UPDATE mailboxes SET used_bytes = GREATEST(0, used_bytes - $1) WHERE id = $2`,
			msg.Size, msg.MailboxID); err != nil {
			return 0, fmt.Errorf("give quota back for uid %d: %w", uid, err)
		}
		removed++
	}

	return removed, tx.Commit(ctx)
}

// Move relocates one message to another folder of the same mailbox and returns
// the UID it has there. The message keeps its identity, its flags and its blob;
// only the folder and the UID change, because a UID means something only inside
// one folder. The quota does not move: the bytes stay in the same mailbox.
func (s *MailStore) Move(ctx context.Context, sourceFolderID string, uid uint32, destFolderID string) (uint32, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if err := lockFolders(ctx, tx, sourceFolderID, destFolderID); err != nil {
		return 0, err
	}
	msg, err := loadMessage(ctx, tx, sourceFolderID, uid)
	if err != nil {
		return 0, err
	}

	var destUID int64
	if err := tx.QueryRow(ctx, `SELECT uid_next FROM folders WHERE id = $1`, destFolderID).Scan(&destUID); err != nil {
		return 0, fmt.Errorf("read destination uid: %w", err)
	}

	unread := 0
	if !msg.Seen {
		unread = 1
	}

	var modseq int64
	if err := tx.QueryRow(ctx, `
		UPDATE folders
		   SET uid_next = uid_next + 1, highest_modseq = highest_modseq + 1,
		       total_count = total_count + 1, unread_count = unread_count + $2
		 WHERE id = $1
		RETURNING highest_modseq`, destFolderID, unread).Scan(&modseq); err != nil {
		return 0, fmt.Errorf("advance destination folder: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE email_messages SET folder_id = $3, uid = $4, modseq = $5 WHERE id = $1 AND received_at = $2`,
		msg.ID, msg.ReceivedAt, destFolderID, destUID, modseq); err != nil {
		return 0, fmt.Errorf("move message: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE folders
		   SET total_count = GREATEST(0, total_count - 1),
		       unread_count = GREATEST(0, unread_count - $2),
		       highest_modseq = highest_modseq + 1
		 WHERE id = $1`, sourceFolderID, unread); err != nil {
		return 0, fmt.Errorf("lower source folder counters: %w", err)
	}

	return uint32(destUID), tx.Commit(ctx)
}

// Copy duplicates messages into another folder. Each copy is its own message
// (own UID, own flags copied from the original) that shares the original's
// blob, and it counts against the quota like any other message. One transaction
// per message, so a failure part-way leaves the earlier copies whole.
func (s *MailStore) Copy(ctx context.Context, sourceFolderID string, uids []uint32, destFolderID string) ([]port.CopiedMessage, error) {
	results := make([]port.CopiedMessage, 0, len(uids))
	for _, uid := range uids {
		destUID, err := s.copyOne(ctx, sourceFolderID, uid, destFolderID)
		if err != nil {
			return nil, fmt.Errorf("copy uid %d: %w", uid, err)
		}
		results = append(results, port.CopiedMessage{SourceUID: uid, DestUID: destUID})
	}
	return results, nil
}

func (s *MailStore) copyOne(ctx context.Context, sourceFolderID string, sourceUID uint32, destFolderID string) (uint32, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockFolders(ctx, tx, destFolderID); err != nil {
		return 0, err
	}
	src, err := loadMessage(ctx, tx, sourceFolderID, sourceUID)
	if err != nil {
		return 0, err
	}

	var destUID int64
	var destMailboxID string
	if err := tx.QueryRow(ctx, `SELECT uid_next, mailbox_id::text FROM folders WHERE id = $1`, destFolderID).Scan(&destUID, &destMailboxID); err != nil {
		return 0, fmt.Errorf("read destination folder: %w", err)
	}

	unread := 0
	if !src.Seen {
		unread = 1
	}
	var modseq int64
	if err := tx.QueryRow(ctx, `
		UPDATE folders
		   SET uid_next = uid_next + 1, highest_modseq = highest_modseq + 1,
		       total_count = total_count + 1, unread_count = unread_count + $2
		 WHERE id = $1
		RETURNING highest_modseq`, destFolderID, unread).Scan(&modseq); err != nil {
		return 0, fmt.Errorf("advance destination folder: %w", err)
	}

	var newID uuid.UUID
	var newReceivedAt any
	if err := tx.QueryRow(ctx, `
		INSERT INTO email_messages (
			mailbox_id, folder_id, uid, modseq, blob_id,
			sender_address, recipient_addresses, size_bytes,
			spf_result, dkim_result, dmarc_result,
			from_display_name, subject, snippet, message_id_header, in_reply_to, thread_id, has_attachments,
			arc_result, dane_status, tls_version, spam_score, spam_verdict, virus_name
		)
		SELECT $2::uuid, $3::uuid, $4::bigint, $5::bigint, blob_id,
			sender_address, recipient_addresses, size_bytes,
			spf_result, dkim_result, dmarc_result,
			from_display_name, subject, snippet, message_id_header, in_reply_to, thread_id, has_attachments,
			arc_result, dane_status, tls_version, spam_score, spam_verdict, virus_name
		  FROM email_messages WHERE id = $1 AND received_at = $6
		RETURNING id, received_at`,
		src.ID, destMailboxID, destFolderID, destUID, modseq, src.ReceivedAt).Scan(&newID, &newReceivedAt); err != nil {
		return 0, fmt.Errorf("insert copied message: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE message_blobs SET ref_count = ref_count + 1 WHERE id = (SELECT blob_id FROM email_messages WHERE id = $1 AND received_at = $2)`,
		newID, newReceivedAt); err != nil {
		return 0, fmt.Errorf("share the blob: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE mailboxes SET used_bytes = used_bytes + $1 WHERE id = $2`, src.Size, destMailboxID); err != nil {
		return 0, fmt.Errorf("count the copy against the quota: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO message_flags (message_id, received_at, flag)
		SELECT $1, $2, flag FROM message_flags WHERE message_id = $3 AND received_at = $4`,
		newID, newReceivedAt, src.ID, src.ReceivedAt); err != nil {
		return 0, fmt.Errorf("copy flags: %w", err)
	}

	return uint32(destUID), tx.Commit(ctx)
}

// DeleteFolder removes a folder the user made, with the mail in it. The
// messages are expunged rather than deleted (recoverable, like any expunge) and
// their bytes go back to the quota.
func (s *MailStore) DeleteFolder(ctx context.Context, folderID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var mailboxID string
	if err := tx.QueryRow(ctx, `SELECT mailbox_id::text FROM folders WHERE id = $1 FOR UPDATE`, folderID).Scan(&mailboxID); err != nil {
		return fmt.Errorf("lock folder: %w", err)
	}

	var freed int64
	if err := tx.QueryRow(ctx, `
		WITH gone AS (
			UPDATE email_messages SET expunged_at = NOW()
			 WHERE folder_id = $1 AND expunged_at IS NULL
			RETURNING size_bytes)
		SELECT COALESCE(SUM(size_bytes), 0) FROM gone`, folderID).Scan(&freed); err != nil {
		return fmt.Errorf("expunge the folder's messages: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE mailboxes SET used_bytes = GREATEST(0, used_bytes - $1) WHERE id = $2`, freed, mailboxID); err != nil {
		return fmt.Errorf("give quota back: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM folders WHERE id = $1`, folderID); err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	return tx.Commit(ctx)
}
