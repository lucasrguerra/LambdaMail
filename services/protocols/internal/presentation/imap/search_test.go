package imappresentation

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

func TestSession_Search_MatchesByFlag(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1"},
	}}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{
		{UID: 1, ReceivedAt: time.Now(), Flags: []string{"\\Seen"}},
		{UID: 2, ReceivedAt: time.Now(), Flags: nil},
		{UID: 3, ReceivedAt: time.Now(), Flags: []string{"\\Seen", "\\Flagged"}},
	}}
	sess := newTestSession(auth, folders, messages, &fakeFlagRepository{}, &fakeBlobReader{})
	_ = sess.Login("user@example.test", testPassword)
	if _, err := sess.Select("INBOX", &imap.SelectOptions{}); err != nil {
		t.Fatalf("Select: %v", err)
	}

	data, err := sess.Search(imapserver.NumKindUID, &imap.SearchCriteria{Flag: []imap.Flag{imap.FlagSeen}}, &imap.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	uidSet, ok := data.All.(imap.UIDSet)
	if !ok {
		t.Fatalf("data.All is %T, want imap.UIDSet", data.All)
	}
	nums, ok := uidSet.Nums()
	if !ok || len(nums) != 2 || nums[0] != 1 || nums[1] != 3 {
		t.Errorf("matched UIDs = %v (ok=%v), want [1 3]", nums, ok)
	}
}

func TestSession_Search_MatchesByUIDSetIntersection(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{"INBOX": {ID: "folder-1"}}}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{
		{UID: 1, ReceivedAt: time.Now()},
		{UID: 2, ReceivedAt: time.Now()},
		{UID: 3, ReceivedAt: time.Now()},
	}}
	sess := newTestSession(auth, folders, messages, &fakeFlagRepository{}, &fakeBlobReader{})
	_ = sess.Login("user@example.test", testPassword)
	_, _ = sess.Select("INBOX", &imap.SelectOptions{})

	var uidFilter imap.UIDSet
	uidFilter.AddRange(2, 0) // "2:*"
	data, err := sess.Search(imapserver.NumKindUID, &imap.SearchCriteria{UID: []imap.UIDSet{uidFilter}}, &imap.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	uidSet := data.All.(imap.UIDSet)
	nums, _ := uidSet.Nums()
	if len(nums) != 2 || nums[0] != 2 || nums[1] != 3 {
		t.Errorf("matched UIDs = %v, want [2 3]", nums)
	}
}

func TestSession_Search_MatchesByHeaderField(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{"INBOX": {ID: "folder-1"}}}
	blobA := uuid.New()
	blobB := uuid.New()
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{
		{UID: 1, BlobID: blobA, ReceivedAt: time.Now()},
		{UID: 2, BlobID: blobB, ReceivedAt: time.Now()},
	}}
	blobs := &fakeBlobReader{content: map[uuid.UUID][]byte{
		blobA: []byte("Subject: hello world\r\n\r\nbody"),
		blobB: []byte("Subject: something else\r\n\r\nbody"),
	}}
	sess := newTestSession(auth, folders, messages, &fakeFlagRepository{}, blobs)
	_ = sess.Login("user@example.test", testPassword)
	_, _ = sess.Select("INBOX", &imap.SelectOptions{})

	data, err := sess.Search(imapserver.NumKindUID, &imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "hello"}},
	}, &imap.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	uidSet := data.All.(imap.UIDSet)
	nums, _ := uidSet.Nums()
	if len(nums) != 1 || nums[0] != 1 {
		t.Errorf("matched UIDs = %v, want [1] (only the message with \"hello\" in Subject)", nums)
	}
}

func TestSession_Search_NotCriteriaExcludesMatches(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{"INBOX": {ID: "folder-1"}}}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{
		{UID: 1, ReceivedAt: time.Now(), Flags: []string{"\\Seen"}},
		{UID: 2, ReceivedAt: time.Now(), Flags: nil},
	}}
	sess := newTestSession(auth, folders, messages, &fakeFlagRepository{}, &fakeBlobReader{})
	_ = sess.Login("user@example.test", testPassword)
	_, _ = sess.Select("INBOX", &imap.SelectOptions{})

	data, err := sess.Search(imapserver.NumKindUID, &imap.SearchCriteria{
		Not: []imap.SearchCriteria{{Flag: []imap.Flag{imap.FlagSeen}}},
	}, &imap.SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	uidSet := data.All.(imap.UIDSet)
	nums, _ := uidSet.Nums()
	if len(nums) != 1 || nums[0] != 2 {
		t.Errorf("matched UIDs = %v, want [2] (NOT SEEN excludes the \\Seen message)", nums)
	}
}
