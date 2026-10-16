package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// The mailbox state contract.
//
// A message can be read, flagged, moved, copied and deleted from IMAP, from the
// webmail, and by the server itself. All of them change the same rows, and the
// rows carry cached numbers (folder counters, the folder's modseq, the
// mailbox's used bytes) that every path must keep true. Before there was one
// implementation, each path kept them true on its own, and they drifted:
//
//   - reading a message over IMAP never moved the unread counter, so the badge
//     in the webmail and IMAP STATUS disagreed with the flags;
//   - deleting from the webmail never gave the quota back;
//   - a second expunge of the same message could push a counter below zero.
//
// These tests state what must hold after any operation and run every operation
// through each path that can perform it. They do not care which code did the
// work, only that the result is the same.

// folderState is everything observable about one folder.
type folderState struct {
	Name          string
	CachedTotal   int
	CachedUnread  int
	RealTotal     int
	RealUnread    int
	HighestModSeq int64
	UIDNext       int64
	MaxMessageSeq int64
}

// mailState reads the cached and the real numbers side by side.
func mailState(t *testing.T, pool *pgxpool.Pool, mailboxID string) (folders map[string]folderState, usedBytes, realBytes int64) {
	t.Helper()
	ctx := context.Background()

	rows, err := pool.Query(ctx, `
		SELECT f.name, f.total_count, f.unread_count, f.highest_modseq, f.uid_next,
		       COUNT(e.id)::int,
		       COUNT(e.id) FILTER (WHERE NOT EXISTS (
		           SELECT 1 FROM message_flags mf
		            WHERE mf.message_id = e.id AND mf.received_at = e.received_at AND mf.flag = '\Seen'))::int,
		       COALESCE(MAX(e.modseq), 0)
		  FROM folders f
		  LEFT JOIN email_messages e ON e.folder_id = f.id AND e.expunged_at IS NULL
		 WHERE f.mailbox_id = $1
		 GROUP BY f.id`, mailboxID)
	if err != nil {
		t.Fatalf("read folder state: %v", err)
	}
	defer rows.Close()

	folders = map[string]folderState{}
	for rows.Next() {
		var s folderState
		if err := rows.Scan(&s.Name, &s.CachedTotal, &s.CachedUnread, &s.HighestModSeq, &s.UIDNext, &s.RealTotal, &s.RealUnread, &s.MaxMessageSeq); err != nil {
			t.Fatalf("scan folder state: %v", err)
		}
		folders[s.Name] = s
	}

	if err := pool.QueryRow(ctx, `SELECT used_bytes FROM mailboxes WHERE id = $1`, mailboxID).Scan(&usedBytes); err != nil {
		t.Fatalf("read used_bytes: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM email_messages WHERE mailbox_id = $1 AND expunged_at IS NULL`, mailboxID).Scan(&realBytes); err != nil {
		t.Fatalf("read real bytes: %v", err)
	}
	return folders, usedBytes, realBytes
}

// assertConsistent is the contract: every cached number equals what the rows say.
func assertConsistent(t *testing.T, pool *pgxpool.Pool, mailboxID, after string) {
	t.Helper()
	folders, used, real := mailState(t, pool, mailboxID)

	for name, f := range folders {
		if f.CachedTotal != f.RealTotal {
			t.Errorf("after %s: %s total_count = %d, but it holds %d messages", after, name, f.CachedTotal, f.RealTotal)
		}
		if f.CachedUnread != f.RealUnread {
			t.Errorf("after %s: %s unread_count = %d, but %d messages are unread", after, name, f.CachedUnread, f.RealUnread)
		}
		if f.CachedUnread < 0 || f.CachedTotal < 0 {
			t.Errorf("after %s: %s has a negative counter (unread %d, total %d)", after, name, f.CachedUnread, f.CachedTotal)
		}
		if f.HighestModSeq < f.MaxMessageSeq {
			t.Errorf("after %s: %s highest_modseq %d is behind a message's modseq %d, so a CONDSTORE client would miss the change", after, name, f.HighestModSeq, f.MaxMessageSeq)
		}
	}
	if used != real {
		t.Errorf("after %s: mailbox used_bytes = %d, but its messages add up to %d", after, used, real)
	}
}

// modseqOf is the folder's highest_modseq, which every observable change must raise.
func modseqOf(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string) int64 {
	t.Helper()
	folders, _, _ := mailState(t, pool, mailboxID)
	return folders[folder].HighestModSeq
}

func folderID(t *testing.T, pool *pgxpool.Pool, mailboxID, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM folders WHERE mailbox_id = $1 AND name = $2`, mailboxID, name).Scan(&id); err != nil {
		t.Fatalf("find folder %s: %v", name, err)
	}
	return id
}

// path is one way of doing each thing to a message. IMAP and webmail both
// implement it; the contract runs against each.
type path struct {
	name      string
	setSeen   func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, seen bool)
	setFlag   func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, flagged bool)
	expunge   func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32)
	moveTo    func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, target string)
	flagsKept func() bool
}

func imapPath() path {
	return path{
		name: "imap",
		setSeen: func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, seen bool) {
			op := port.FlagOpAdd
			if !seen {
				op = port.FlagOpDel
			}
			if _, err := NewFlagRepository(pool).SetFlags(context.Background(), folderID(t, pool, mailboxID, folder), uid, op, []string{`\Seen`}, 0); err != nil {
				t.Fatalf("imap SetFlags: %v", err)
			}
		},
		setFlag: func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, flagged bool) {
			op := port.FlagOpAdd
			if !flagged {
				op = port.FlagOpDel
			}
			if _, err := NewFlagRepository(pool).SetFlags(context.Background(), folderID(t, pool, mailboxID, folder), uid, op, []string{`\Flagged`}, 0); err != nil {
				t.Fatalf("imap SetFlags: %v", err)
			}
		},
		expunge: func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32) {
			if err := NewExpungeRepository(pool).Expunge(context.Background(), folderID(t, pool, mailboxID, folder), []uint32{uid}); err != nil {
				t.Fatalf("imap Expunge: %v", err)
			}
		},
		moveTo: func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, target string) {
			// IMAP has no MOVE here: a client copies, marks \Deleted and expunges
			src, dst := folderID(t, pool, mailboxID, folder), folderID(t, pool, mailboxID, target)
			if _, err := NewCopyRepository(pool).CopyMessages(context.Background(), src, []uint32{uid}, dst); err != nil {
				t.Fatalf("imap Copy: %v", err)
			}
			if err := NewExpungeRepository(pool).Expunge(context.Background(), src, []uint32{uid}); err != nil {
				t.Fatalf("imap Expunge after copy: %v", err)
			}
		},
	}
}

func webmailPath() path {
	return path{
		name: "webmail",
		setSeen: func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, seen bool) {
			if err := NewWebmailRepository(pool).MarkSeen(context.Background(), mailboxID, folder, uid, seen); err != nil {
				t.Fatalf("webmail MarkSeen: %v", err)
			}
		},
		setFlag: func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, flagged bool) {
			if err := NewWebmailRepository(pool).MarkFlagged(context.Background(), mailboxID, folder, uid, flagged); err != nil {
				t.Fatalf("webmail MarkFlagged: %v", err)
			}
		},
		expunge: func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32) {
			if err := NewWebmailRepository(pool).Expunge(context.Background(), mailboxID, folder, uid); err != nil {
				t.Fatalf("webmail Expunge: %v", err)
			}
		},
		moveTo: func(t *testing.T, pool *pgxpool.Pool, mailboxID, folder string, uid uint32, target string) {
			if _, err := NewWebmailRepository(pool).MoveToFolder(context.Background(), mailboxID, folder, uid, target); err != nil {
				t.Fatalf("webmail Move: %v", err)
			}
		},
	}
}

func eachPath(t *testing.T, scenario func(t *testing.T, p path)) {
	for _, p := range []path{imapPath(), webmailPath()} {
		p := p
		t.Run(p.name, func(t *testing.T) { scenario(t, p) })
	}
}

func TestContract_ReadingAMessageMovesTheUnreadCounter(t *testing.T) {
	pool := testPool(t)
	eachPath(t, func(t *testing.T, p path) {
		mailboxID := seedMailbox(t, pool)
		a := deliver(t, pool, mailboxID, "INBOX", "a", false)
		deliver(t, pool, mailboxID, "INBOX", "b", false)
		assertConsistent(t, pool, mailboxID, "delivery")

		before := modseqOf(t, pool, mailboxID, "INBOX")
		p.setSeen(t, pool, mailboxID, "INBOX", a, true)
		assertConsistent(t, pool, mailboxID, "marking one message read")
		if after := modseqOf(t, pool, mailboxID, "INBOX"); after <= before {
			t.Errorf("marking read left highest_modseq at %d (was %d): clients holding the folder never learn of it", after, before)
		}

		p.setSeen(t, pool, mailboxID, "INBOX", a, true)
		assertConsistent(t, pool, mailboxID, "marking the same message read twice")

		p.setSeen(t, pool, mailboxID, "INBOX", a, false)
		assertConsistent(t, pool, mailboxID, "marking it unread again")
	})
}

func TestContract_FlaggingBumpsTheModseqAndNothingElse(t *testing.T) {
	pool := testPool(t)
	eachPath(t, func(t *testing.T, p path) {
		mailboxID := seedMailbox(t, pool)
		a := deliver(t, pool, mailboxID, "INBOX", "a", false)

		before := modseqOf(t, pool, mailboxID, "INBOX")
		p.setFlag(t, pool, mailboxID, "INBOX", a, true)
		assertConsistent(t, pool, mailboxID, "flagging")
		if after := modseqOf(t, pool, mailboxID, "INBOX"); after <= before {
			t.Errorf("flagging left highest_modseq at %d (was %d)", after, before)
		}
		p.setFlag(t, pool, mailboxID, "INBOX", a, false)
		assertConsistent(t, pool, mailboxID, "unflagging")
	})
}

func TestContract_DeletingFreesTheQuotaAndTheCounters(t *testing.T) {
	pool := testPool(t)
	eachPath(t, func(t *testing.T, p path) {
		mailboxID := seedMailbox(t, pool)
		unread := deliver(t, pool, mailboxID, "INBOX", "unread", false)
		read := deliver(t, pool, mailboxID, "INBOX", "read", true)
		assertConsistent(t, pool, mailboxID, "delivery")

		p.expunge(t, pool, mailboxID, "INBOX", unread)
		assertConsistent(t, pool, mailboxID, "deleting an unread message")

		p.expunge(t, pool, mailboxID, "INBOX", read)
		assertConsistent(t, pool, mailboxID, "deleting a read message")

		_, used, _ := mailState(t, pool, mailboxID)
		if used != 0 {
			t.Errorf("an emptied mailbox still reports %d used bytes", used)
		}
	})
}

func TestContract_AMessageAlreadyDeletedCannotDriveACounterNegative(t *testing.T) {
	pool := testPool(t)
	eachPath(t, func(t *testing.T, p path) {
		mailboxID := seedMailbox(t, pool)
		a := deliver(t, pool, mailboxID, "INBOX", "a", false)
		deliver(t, pool, mailboxID, "INBOX", "b", false)

		p.expunge(t, pool, mailboxID, "INBOX", a)
		// The second one is the same message again. The webmail reports it as
		// not found; IMAP ignores it. Either way no number may move.
		func() {
			defer func() { _ = recover() }()
			expungeAgain(t, pool, p, mailboxID, a)
		}()
		assertConsistent(t, pool, mailboxID, "deleting the same message twice")
	})
}

// expungeAgain tolerates the webmail's "not found" while still letting the
// contract check the counters afterwards.
func expungeAgain(t *testing.T, pool *pgxpool.Pool, p path, mailboxID string, uid uint32) {
	t.Helper()
	switch p.name {
	case "imap":
		_ = NewExpungeRepository(pool).Expunge(context.Background(), folderID(t, pool, mailboxID, "INBOX"), []uint32{uid})
	default:
		_ = NewWebmailRepository(pool).Expunge(context.Background(), mailboxID, "INBOX", uid)
	}
}

func TestContract_MovingAMessageKeepsBothFoldersTrue(t *testing.T) {
	pool := testPool(t)
	eachPath(t, func(t *testing.T, p path) {
		mailboxID := seedMailbox(t, pool)
		unread := deliver(t, pool, mailboxID, "INBOX", "unread", false)
		read := deliver(t, pool, mailboxID, "INBOX", "read", true)

		p.moveTo(t, pool, mailboxID, "INBOX", unread, "Trash")
		assertConsistent(t, pool, mailboxID, "moving an unread message")
		p.moveTo(t, pool, mailboxID, "INBOX", read, "Trash")
		assertConsistent(t, pool, mailboxID, "moving a read message")

		folders, _, _ := mailState(t, pool, mailboxID)
		if folders["INBOX"].RealTotal != 0 || folders["Trash"].RealTotal != 2 {
			t.Errorf("INBOX holds %d and Trash %d, want 0 and 2", folders["INBOX"].RealTotal, folders["Trash"].RealTotal)
		}
	})
}

// Reading mail over IMAP and then looking at it in the webmail (or the other way
// round) is the everyday case: the two views must agree after every step.
func TestContract_ImapAndWebmailAgreeOnTheSameMailbox(t *testing.T) {
	pool := testPool(t)
	imap, web := imapPath(), webmailPath()

	mailboxID := seedMailbox(t, pool)
	var uids []uint32
	for i := 0; i < 5; i++ {
		uids = append(uids, deliver(t, pool, mailboxID, "INBOX", fmt.Sprintf("m%d", i), false))
	}

	imap.setSeen(t, pool, mailboxID, "INBOX", uids[0], true)
	assertConsistent(t, pool, mailboxID, "reading m0 over IMAP")
	web.setSeen(t, pool, mailboxID, "INBOX", uids[1], true)
	assertConsistent(t, pool, mailboxID, "reading m1 in the webmail")
	imap.setFlag(t, pool, mailboxID, "INBOX", uids[2], true)
	web.expunge(t, pool, mailboxID, "INBOX", uids[3])
	assertConsistent(t, pool, mailboxID, "deleting m3 in the webmail")
	imap.expunge(t, pool, mailboxID, "INBOX", uids[1])
	assertConsistent(t, pool, mailboxID, "deleting the already-read m1 over IMAP")
	web.moveTo(t, pool, mailboxID, "INBOX", uids[4], "Trash")
	assertConsistent(t, pool, mailboxID, "moving m4 to Trash in the webmail")
	imap.moveTo(t, pool, mailboxID, "INBOX", uids[2], "Trash")
	assertConsistent(t, pool, mailboxID, "moving m2 to Trash over IMAP")

	// What the webmail reports for a folder is what IMAP would answer
	ctx := context.Background()
	wmFolders, err := NewWebmailRepository(pool).ListFolders(ctx, mailboxID)
	if err != nil {
		t.Fatalf("webmail ListFolders: %v", err)
	}
	imFolders, err := NewImapFolderRepository(pool).ListFolders(ctx, mailboxID)
	if err != nil {
		t.Fatalf("imap ListFolders: %v", err)
	}
	imapTotals := map[string]int{}
	for _, f := range imFolders {
		imapTotals[f.Name] = int(f.NumMessages)
	}
	for _, f := range wmFolders {
		if got := imapTotals[f.Name]; got != f.TotalCount {
			t.Errorf("%s: the webmail shows %d messages and IMAP says %d", f.Name, f.TotalCount, got)
		}
	}
}

// A folder deleted by its owner takes its messages with it, and their bytes.
func TestContract_DeletingAFolderReturnsItsBytes(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	mailboxID := seedMailbox(t, pool)
	repo := NewWebmailRepository(pool)

	if err := repo.CreateFolder(ctx, mailboxID, "Projetos"); err != nil {
		t.Fatalf("create folder: %v", err)
	}
	deliver(t, pool, mailboxID, "Projetos", "one", false)
	deliver(t, pool, mailboxID, "Projetos", "two", true)
	deliver(t, pool, mailboxID, "INBOX", "stays", false)
	assertConsistent(t, pool, mailboxID, "filing into a new folder")

	if err := repo.DeleteFolder(ctx, mailboxID, "Projetos"); err != nil {
		t.Fatalf("delete folder: %v", err)
	}
	assertConsistent(t, pool, mailboxID, "deleting the folder")
}

// IMAP's STORE can replace a message's whole flag set; the unread counter has to
// follow the \Seen that comes or goes with it.
func TestContract_ReplacingTheFlagSetKeepsTheUnreadCounterTrue(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	mailboxID := seedMailbox(t, pool)
	a := deliver(t, pool, mailboxID, "INBOX", "a", false)
	b := deliver(t, pool, mailboxID, "INBOX", "b", true)
	inbox := folderID(t, pool, mailboxID, "INBOX")
	repo := NewFlagRepository(pool)

	if _, err := repo.SetFlags(ctx, inbox, a, port.FlagOpSet, []string{`\Seen`, `\Flagged`}, 0); err != nil {
		t.Fatalf("SetFlags: %v", err)
	}
	assertConsistent(t, pool, mailboxID, "replacing flags with one that includes \\Seen")

	if _, err := repo.SetFlags(ctx, inbox, b, port.FlagOpSet, []string{`\Flagged`}, 0); err != nil {
		t.Fatalf("SetFlags: %v", err)
	}
	assertConsistent(t, pool, mailboxID, "replacing flags with one that drops \\Seen")
}

var _ = uuid.Nil

// IMAP clients, the webmail and the delivery path all touch a mailbox at the same
// moment. The counters are shared rows, so they have to stay true under that.
func TestContract_ConcurrentChangesFromBothPathsKeepTheCountersTrue(t *testing.T) {
	pool := testPool(t)
	mailboxID := seedMailbox(t, pool)

	const n = 24
	uids := make([]uint32, 0, n)
	for i := 0; i < n; i++ {
		uids = append(uids, deliver(t, pool, mailboxID, "INBOX", fmt.Sprintf("m%d", i), false))
	}

	imap, web := imapPath(), webmailPath()
	done := make(chan struct{}, n)
	for i, uid := range uids {
		go func(i int, uid uint32) {
			defer func() { done <- struct{}{} }()
			switch i % 3 {
			case 0:
				imap.setSeen(t, pool, mailboxID, "INBOX", uid, true)
			case 1:
				web.setSeen(t, pool, mailboxID, "INBOX", uid, true)
			default:
				if i%2 == 0 {
					imap.expunge(t, pool, mailboxID, "INBOX", uid)
				} else {
					web.expunge(t, pool, mailboxID, "INBOX", uid)
				}
			}
		}(i, uid)
	}
	for range uids {
		<-done
	}

	assertConsistent(t, pool, mailboxID, "24 concurrent changes from IMAP and the webmail")
}

// Two clients deleting the same message at once is the realistic race (the
// webmail and a phone both open on the same mail). It is removed once, and every
// number moves once.
func TestContract_DeletingTheSameMessageConcurrentlyCountsOnce(t *testing.T) {
	pool := testPool(t)
	mailboxID := seedMailbox(t, pool)
	target := deliver(t, pool, mailboxID, "INBOX", "contested", false)
	deliver(t, pool, mailboxID, "INBOX", "bystander", false)
	inbox := folderID(t, pool, mailboxID, "INBOX")

	ctx := context.Background()
	store := NewMailStore(pool)
	removed := make(chan int, 12)
	for i := 0; i < 12; i++ {
		go func() {
			n, err := store.Expunge(ctx, inbox, []uint32{target})
			if err != nil {
				t.Errorf("expunge: %v", err)
			}
			removed <- n
		}()
	}
	total := 0
	for i := 0; i < 12; i++ {
		total += <-removed
	}

	if total != 1 {
		t.Errorf("12 concurrent expunges of one message removed it %d times, want exactly once", total)
	}
	assertConsistent(t, pool, mailboxID, "12 clients deleting the same message")
	folders, used, _ := mailState(t, pool, mailboxID)
	if folders["INBOX"].RealTotal != 1 || used != 10 {
		t.Errorf("INBOX holds %d and the mailbox %d bytes, want 1 message and 10 bytes", folders["INBOX"].RealTotal, used)
	}
}

// Moves in opposite directions, flag changes and deletes, all at once on the same
// two folders. Locking the folders and the message in different orders in
// different operations deadlocks here; a fixed order does not.
func TestContract_OppositeMovesAndFlagChangesNeverDeadlock(t *testing.T) {
	pool := testPool(t)
	mailboxID := seedMailbox(t, pool)

	const each = 20
	var inboxUIDs, trashUIDs []uint32
	for i := 0; i < each; i++ {
		inboxUIDs = append(inboxUIDs, deliver(t, pool, mailboxID, "INBOX", fmt.Sprintf("i%d", i), false))
		trashUIDs = append(trashUIDs, deliver(t, pool, mailboxID, "Trash", fmt.Sprintf("t%d", i), false))
	}

	inbox, trash := folderID(t, pool, mailboxID, "INBOX"), folderID(t, pool, mailboxID, "Trash")
	store := NewMailStore(pool)
	ctx := context.Background()

	errs := make(chan error, each*4)
	var pending int
	run := func(f func() error) {
		pending++
		go func() { errs <- f() }()
	}
	for i := 0; i < each; i++ {
		i := i
		run(func() error { _, err := store.Move(ctx, inbox, inboxUIDs[i], trash); return nil2(err) })
		run(func() error { _, err := store.Move(ctx, trash, trashUIDs[i], inbox); return nil2(err) })
		// a flag change on a message that is being moved elsewhere, and a delete
		run(func() error {
			_, err := store.SetFlags(ctx, inbox, inboxUIDs[(i+1)%each], port.FlagOpAdd, []string{`\Flagged`}, 0)
			return nil2(err)
		})
		run(func() error { _, err := store.Expunge(ctx, trash, []uint32{trashUIDs[(i+2)%each]}); return nil2(err) })
	}
	for ; pending > 0; pending-- {
		if err := <-errs; err != nil {
			t.Errorf("a concurrent operation failed (a deadlock, if it says 40P01): %v", err)
		}
	}

	assertConsistent(t, pool, mailboxID, "opposite moves, flag changes and deletes all at once")
}

// nil2 lets the contract tolerate "the message was moved or deleted first", a
// legitimate outcome of a race, while still surfacing every other error (above
// all a deadlock, SQLSTATE 40P01).
func nil2(err error) error {
	if err == ErrMessageNotFound {
		return nil
	}
	return err
}
