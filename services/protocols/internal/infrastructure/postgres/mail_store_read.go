package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The read side of MailStore.
//
// IMAP (LIST, SELECT, FETCH) and the webmail (folder list, message list) used to
// read the same tables with different queries, and so could answer differently:
// IMAP took a folder's message count from a cached column, the webmail counted
// the rows. A client comparing the two saw numbers that disagreed. They now ask
// the same question, and the answer is derived from the messages themselves, so
// a folder can only ever report what is actually in it.

// FolderInfo is one folder with the numbers a client needs.
type FolderInfo struct {
	ID            string
	Name          string
	SpecialUse    string
	UIDNext       int64
	UIDValidity   int64
	HighestModSeq int64
	// Total and Unread are counted from the messages, not read from the cached
	// columns: the columns are kept true by every write path, and counting here
	// makes a wrong one visible instead of repeated.
	Total  int
	Unread int
}

const folderColumns = `
	SELECT f.id::text, f.name, COALESCE(f.special_use, ''),
	       f.uid_next, f.uid_validity, f.highest_modseq,
	       COUNT(e.id)::int,
	       COUNT(e.id) FILTER (
	         WHERE NOT EXISTS (SELECT 1 FROM message_flags mf
	                            WHERE mf.message_id = e.id
	                              AND mf.received_at = e.received_at
	                              AND mf.flag = '\Seen')
	       )::int
	  FROM folders f
	  LEFT JOIN email_messages e ON e.folder_id = f.id AND e.expunged_at IS NULL`

func scanFolder(row pgx.Row) (FolderInfo, error) {
	var f FolderInfo
	err := row.Scan(&f.ID, &f.Name, &f.SpecialUse, &f.UIDNext, &f.UIDValidity, &f.HighestModSeq, &f.Total, &f.Unread)
	return f, err
}

// Folders lists a mailbox's folders by name.
func (s *MailStore) Folders(ctx context.Context, mailboxID string) ([]FolderInfo, error) {
	rows, err := s.pool.Query(ctx, folderColumns+`
	 WHERE f.mailbox_id = $1
	 GROUP BY f.id
	 ORDER BY f.name`, mailboxID)
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	defer rows.Close()

	var out []FolderInfo
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FolderByName finds one folder by its exact name; nil, nil when there is none
// (IMAP SELECT treats that as a normal answer, not a failure).
func (s *MailStore) FolderByName(ctx context.Context, mailboxID, name string) (*FolderInfo, error) {
	f, err := scanFolder(s.pool.QueryRow(ctx, folderColumns+`
	 WHERE f.mailbox_id = $1 AND f.name = $2
	 GROUP BY f.id`, mailboxID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// MessageInfo is one message as either a mail client or the webmail lists it.
type MessageInfo struct {
	UID            uint32
	BlobID         uuid.UUID
	Size           int64
	ReceivedAt     time.Time
	ModSeq         uint64
	Flags          []string
	Subject        string
	SenderAddress  string
	FromName       string
	Snippet        string
	HasAttachments bool
	SpamVerdict    string
	DmarcResult    string
}

// HasFlag reports whether the message carries the flag.
func (m MessageInfo) HasFlag(flag string) bool {
	for _, f := range m.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// MessageQuery shapes a listing. The zero value lists every message of the
// folder by UID ascending, which is what IMAP needs to number them.
type MessageQuery struct {
	// NewestFirst orders by arrival, newest first, as the webmail shows them
	NewestFirst bool
	// Search keeps messages whose subject, sender or snippet contains it
	Search string
	// Limit and Offset page the result; Limit 0 means no limit
	Limit  int
	Offset int
}

// Messages lists a folder's live messages with their flags.
func (s *MailStore) Messages(ctx context.Context, folderID string, q MessageQuery) ([]MessageInfo, error) {
	order := `e.uid ASC`
	if q.NewestFirst {
		order = `e.received_at DESC, e.uid DESC`
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 1<<31 - 1
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	rows, err := s.pool.Query(ctx, `
		SELECT e.uid, e.blob_id, e.size_bytes, e.received_at, e.modseq,
		       COALESCE((SELECT array_agg(mf.flag) FROM message_flags mf
		                  WHERE mf.message_id = e.id AND mf.received_at = e.received_at), '{}'),
		       COALESCE(e.subject, ''), e.sender_address::text,
		       COALESCE(e.from_display_name, ''), COALESCE(e.snippet, ''),
		       e.has_attachments, COALESCE(e.spam_verdict, ''), COALESCE(e.dmarc_result, '')
		  FROM email_messages e
		 WHERE e.folder_id = $1 AND e.expunged_at IS NULL
		   AND ($2 = '' OR e.subject ILIKE '%' || $2 || '%'
		                OR e.sender_address::text ILIKE '%' || $2 || '%'
		                OR e.snippet ILIKE '%' || $2 || '%')
		 ORDER BY `+order+`
		 LIMIT $3 OFFSET $4`, folderID, q.Search, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var out []MessageInfo
	for rows.Next() {
		var m MessageInfo
		if err := rows.Scan(&m.UID, &m.BlobID, &m.Size, &m.ReceivedAt, &m.ModSeq, &m.Flags,
			&m.Subject, &m.SenderAddress, &m.FromName, &m.Snippet,
			&m.HasAttachments, &m.SpamVerdict, &m.DmarcResult); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MessageBlob resolves one message to its stored blob, or ErrMessageNotFound.
func (s *MailStore) MessageBlob(ctx context.Context, folderID string, uid uint32) (uuid.UUID, error) {
	var blobID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT blob_id FROM email_messages WHERE folder_id = $1 AND uid = $2 AND expunged_at IS NULL`,
		folderID, int64(uid)).Scan(&blobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrMessageNotFound
	}
	return blobID, err
}

// MailboxIDByAddress resolves an active address to its mailbox id.
func (s *MailStore) MailboxIDByAddress(ctx context.Context, address string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT m.id::text FROM mailboxes m
		  JOIN domains d ON d.id = m.domain_id
		 WHERE m.email_address = $1 AND m.is_active AND d.is_active`, address).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrMessageNotFound
	}
	return id, err
}
