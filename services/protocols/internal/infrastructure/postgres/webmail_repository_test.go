package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
)

// These reproduce the counters the webmail was showing wrong: an unread badge
// that never came down however many messages were read, and a Sent folder that
// advertised unread mail the user had written themselves.

// seedMailbox creates an isolated domain, mailbox and folder set for one test,
// so tests cannot see each other's counters.
func seedMailbox(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	domainID := uuid.New()
	mbID := uuid.New()
	name := "t" + uuid.NewString()[:8] + ".test"

	if _, err := pool.Exec(ctx,
		`INSERT INTO domains (id, name, punycode_name, is_active) VALUES ($1,$2,$3,true)`,
		domainID, name, name); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash, is_active)
		VALUES ($1,$2,'user',$3,'x',true)`,
		mbID, domainID, "user@"+name); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	for _, f := range [][2]string{{"INBOX", "inbox"}, {"Sent", "sent"}, {"Drafts", "drafts"}, {"Trash", "trash"}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1,$2,$3)`,
			mbID, f[0], f[1]); err != nil {
			t.Fatalf("seed folder %s: %v", f[0], err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM domains WHERE id = $1`, domainID)
	})
	return mbID.String()
}

// deliver files a message the way the inbound path does.
func deliver(t *testing.T, pool *pgxpool.Pool, mailboxID, folder, subject string, alreadySeen bool) uint32 {
	t.Helper()
	repo := NewInboundMessageRepository(pool)
	blobID := storeTestBlob(t, pool)
	uids, err := repo.PersistAll(context.Background(), []port.PersistInboundMessageInput{{
		MailboxID:        uuid.MustParse(mailboxID),
		Blob:             port.BlobRef{ID: blobID, SHA256: uuid.NewString(), SizeBytes: 10},
		SenderAddress:    "someone@example.test",
		RecipientAddress: "user@example.test",
		TargetFolderName: folder,
		Subject:          subject,
		AlreadySeen:      alreadySeen,
	}})
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	return uint32(uids[0])
}

func storeTestBlob(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO message_blobs (id, content_sha256, storage_driver, storage_path, size_bytes, ref_count)
		VALUES ($1, $2, 'local', $3, 10, 0)`,
		id, strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")[:64], "/dev/null/"+id.String()); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	return id
}

func folderByRole(t *testing.T, folders []port.WebmailFolder, role string) port.WebmailFolder {
	t.Helper()
	for _, f := range folders {
		if f.SpecialUse == role {
			return f
		}
	}
	t.Fatalf("no %q folder in %v", role, folders)
	return port.WebmailFolder{}
}

// The bug the user reported: read a message, the unread number does not move,
// and it is still wrong after logging out and back in.
func TestUnreadCountFollowsWhatWasActuallyRead(t *testing.T) {
	pool := testPool(t)
	repo := NewWebmailRepository(pool)
	mailboxID := seedMailbox(t, pool)
	ctx := context.Background()

	uid := deliver(t, pool, mailboxID, "INBOX", "first", false)
	deliver(t, pool, mailboxID, "INBOX", "second", false)

	folders, err := repo.ListFolders(ctx, mailboxID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	if got := folderByRole(t, folders, "inbox").UnreadCount; got != 2 {
		t.Fatalf("two unread messages delivered, folder reports %d", got)
	}

	if err := repo.MarkSeen(ctx, mailboxID, "inbox", uid, true); err != nil {
		t.Fatalf("mark seen: %v", err)
	}

	folders, _ = repo.ListFolders(ctx, mailboxID)
	if got := folderByRole(t, folders, "inbox").UnreadCount; got != 1 {
		t.Errorf("after reading one of two, unread is %d, want 1", got)
	}
}

// A counter that has already drifted must correct itself, or the number stays
// wrong forever no matter what the user does.
func TestUnreadCountSelfHealsAfterDrift(t *testing.T) {
	pool := testPool(t)
	repo := NewWebmailRepository(pool)
	mailboxID := seedMailbox(t, pool)
	ctx := context.Background()

	deliver(t, pool, mailboxID, "INBOX", "only one", false)

	// Simulate the drift the old write path produced.
	if _, err := pool.Exec(ctx, `
		UPDATE folders SET unread_count = 47, total_count = 99
		 WHERE mailbox_id = $1 AND special_use = 'inbox'`, mailboxID); err != nil {
		t.Fatalf("force drift: %v", err)
	}

	folders, err := repo.ListFolders(ctx, mailboxID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	inbox := folderByRole(t, folders, "inbox")
	if inbox.UnreadCount != 1 {
		t.Errorf("unread did not self-heal: got %d, want 1", inbox.UnreadCount)
	}
	if inbox.TotalCount != 1 {
		t.Errorf("total did not self-heal: got %d, want 1", inbox.TotalCount)
	}
}

// A message the user wrote themselves is not unread mail.
func TestOwnSentCopyIsNotCountedAsUnread(t *testing.T) {
	pool := testPool(t)
	repo := NewWebmailRepository(pool)
	mailboxID := seedMailbox(t, pool)

	deliver(t, pool, mailboxID, "Sent", "something I wrote", true)

	folders, err := repo.ListFolders(context.Background(), mailboxID)
	if err != nil {
		t.Fatalf("list folders: %v", err)
	}
	sent := folderByRole(t, folders, "sent")
	if sent.UnreadCount != 0 {
		t.Errorf("the Sent folder reports %d unread; a message you sent is not unread mail", sent.UnreadCount)
	}
	if sent.TotalCount != 1 {
		t.Errorf("Sent total is %d, want 1", sent.TotalCount)
	}
}

// Deleting from an ordinary folder moves the message to Trash rather than
// destroying it, which is what every mail client does and what makes the
// action safe to offer.
func TestDeleteMovesToTrashThenExpungesFromTrash(t *testing.T) {
	pool := testPool(t)
	repo := NewWebmailRepository(pool)
	mailboxID := seedMailbox(t, pool)
	ctx := context.Background()

	uid := deliver(t, pool, mailboxID, "INBOX", "unwanted", false)

	trashUID, err := repo.MoveToTrash(ctx, mailboxID, "inbox", uid)
	if err != nil {
		t.Fatalf("move to trash: %v", err)
	}

	inboxRows, _ := repo.ListMessages(ctx, mailboxID, "inbox", "", 50, 0)
	if len(inboxRows) != 0 {
		t.Errorf("the message is still in the inbox after being deleted: %d rows", len(inboxRows))
	}
	trashRows, _ := repo.ListMessages(ctx, mailboxID, "trash", "", 50, 0)
	if len(trashRows) != 1 {
		t.Fatalf("the deleted message did not arrive in Trash: %d rows", len(trashRows))
	}

	folders, _ := repo.ListFolders(ctx, mailboxID)
	if got := folderByRole(t, folders, "inbox").UnreadCount; got != 0 {
		t.Errorf("inbox still counts the moved message as unread: %d", got)
	}

	if err := repo.Expunge(ctx, mailboxID, "trash", trashUID); err != nil {
		t.Fatalf("expunge from trash: %v", err)
	}
	trashRows, _ = repo.ListMessages(ctx, mailboxID, "trash", "", 50, 0)
	if len(trashRows) != 0 {
		t.Errorf("the message survived being emptied from Trash: %d rows", len(trashRows))
	}
}
