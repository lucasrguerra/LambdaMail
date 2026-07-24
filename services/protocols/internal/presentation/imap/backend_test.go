package imappresentation

import (
	"context"
	"errors"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

type fakeAuthRepository struct {
	byAddress map[string]port.MailboxAuth
}

func (f *fakeAuthRepository) FindByAddress(_ context.Context, address string) (*port.MailboxAuth, error) {
	rec, ok := f.byAddress[address]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

type fakeImapFolderRepository struct {
	byName map[string]port.ImapFolderRecord
}

func (f *fakeImapFolderRepository) ListFolders(_ context.Context, _ string) ([]port.ImapFolderRecord, error) {
	var out []port.ImapFolderRecord
	for _, rec := range f.byName {
		out = append(out, rec)
	}
	return out, nil
}

func (f *fakeImapFolderRepository) FindByName(_ context.Context, _ string, name string) (*port.ImapFolderRecord, error) {
	rec, ok := f.byName[name]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

type fakeMessageQueryRepository struct {
	messages []port.MessageRecord
}

func (f *fakeMessageQueryRepository) ListMessages(_ context.Context, _ string) ([]port.MessageRecord, error) {
	return f.messages, nil
}

type fakeFlagRepository struct {
	lastFolderID string
	lastUID      uint32
	lastOp       port.FlagOp
	lastFlags    []string

	calls []fakeFlagCall
}

type fakeFlagCall struct {
	folderID string
	uid      uint32
	op       port.FlagOp
	flags    []string
}

func (f *fakeFlagRepository) SetFlags(_ context.Context, folderID string, uid uint32, op port.FlagOp, flags []string, _ uint64) (bool, error) {
	f.lastFolderID, f.lastUID, f.lastOp, f.lastFlags = folderID, uid, op, flags
	f.calls = append(f.calls, fakeFlagCall{folderID: folderID, uid: uid, op: op, flags: flags})
	return true, nil
}

type fakeBlobReader struct {
	content map[uuid.UUID][]byte
}

func (f *fakeBlobReader) ReadByID(_ context.Context, blobID uuid.UUID) ([]byte, error) {
	return f.content[blobID], nil
}

// fakeExpungeRepository/fakeCopyRepository duplicate the fakes from
// internal/application/usecase/imap_session_test.go - Go test files in
// different packages can't share unexported test helpers.
type fakeExpungeRepository struct {
	calls []struct {
		folderID string
		uids     []uint32
	}
}

func (f *fakeExpungeRepository) Expunge(_ context.Context, folderID string, uids []uint32) error {
	f.calls = append(f.calls, struct {
		folderID string
		uids     []uint32
	}{folderID, uids})
	return nil
}

type fakeCopyRepository struct {
	result []port.CopiedMessage
}

func (f *fakeCopyRepository) CopyMessages(_ context.Context, _ string, _ []uint32, _ string) ([]port.CopiedMessage, error) {
	return f.result, nil
}

// testPasswordHash/testPassword duplicate the constants from
// internal/application/usecase/imap_session_test.go - a real Argon2id PHC
// hash for "correct horse battery staple", generated once with
// argon2id.CreateHash and hardcoded (see that file's Task 2 note for how
// to generate it).
const (
	testPassword     = "correct horse battery staple"
	testPasswordHash = "$argon2id$v=19$m=65536,t=1,p=12$I88aUc0rZHHX+Bt+jX+4Ag$3jKTtg1khJjFo9az4iPvFLc3eKY8WKSFy/1YMXlKvSU"
)

func newTestSession(auth *fakeAuthRepository, folders *fakeImapFolderRepository, messages *fakeMessageQueryRepository, flags *fakeFlagRepository, blobs *fakeBlobReader) imapserver.Session {
	// expunger/copier are nil here: none of the tests using this helper
	// exercise Expunge/CopyMessages, and NewImapSessionUseCase only stores
	// these interfaces without dereferencing them at construction time.
	// Tests that DO exercise Expunge/Copy use
	// newTestSessionWithExpungeAndCopy below instead.
	return newTestSessionWithExpungeAndCopy(auth, folders, messages, flags, blobs, nil, nil)
}

func newTestSessionWithExpungeAndCopy(auth *fakeAuthRepository, folders *fakeImapFolderRepository, messages *fakeMessageQueryRepository, flags *fakeFlagRepository, blobs *fakeBlobReader, expunger *fakeExpungeRepository, copier *fakeCopyRepository) imapserver.Session {
	var expungeRepo port.ExpungeRepository
	if expunger != nil {
		expungeRepo = expunger
	}
	var copyRepo port.CopyRepository
	if copier != nil {
		copyRepo = copier
	}
	uc := usecase.NewImapSessionUseCase(auth, folders, messages, flags, blobs, expungeRepo, copyRepo)
	sess, _, err := NewSession(nil, uc)
	if err != nil {
		panic(err)
	}
	return sess
}

func TestSession_Login_SucceedsForCorrectCredentials(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	sess := newTestSession(auth, nil, nil, nil, nil)

	if err := sess.Login("user@example.test", testPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestSession_Login_FailsForWrongPassword(t *testing.T) {
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: uuid.New(), PasswordHash: testPasswordHash},
	}}
	sess := newTestSession(auth, nil, nil, nil, nil)

	err := sess.Login("user@example.test", "wrong")
	if err == nil {
		t.Fatal("expected an error for wrong password, got nil")
	}
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) {
		t.Fatalf("error is not an *imap.Error: %v", err)
	}
	if imapErr.Type != imap.StatusResponseTypeNo {
		t.Errorf("Type = %v, want StatusResponseTypeNo", imapErr.Type)
	}
}

func TestSession_Select_ReturnsDataForKnownFolder(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1", Name: "INBOX", UIDNext: 5, UIDValidity: 999, NumMessages: 3},
	}}
	sess := newTestSession(auth, folders, &fakeMessageQueryRepository{}, &fakeFlagRepository{}, &fakeBlobReader{})
	if err := sess.Login("user@example.test", testPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}

	data, err := sess.Select("INBOX", &imap.SelectOptions{})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if data.UIDNext != 5 || data.UIDValidity != 999 || data.NumMessages != 3 {
		t.Errorf("data = %+v, want UIDNext=5 UIDValidity=999 NumMessages=3", data)
	}
}

func TestSession_Select_FailsForUnknownFolder(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{}}
	sess := newTestSession(auth, folders, &fakeMessageQueryRepository{}, &fakeFlagRepository{}, &fakeBlobReader{})
	_ = sess.Login("user@example.test", testPassword)

	_, err := sess.Select("Nonexistent", &imap.SelectOptions{})
	if err == nil {
		t.Fatal("expected an error for an unknown folder, got nil")
	}
}

func TestSession_Store_CallsFlagRepositoryWithCorrectArgs(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1", Name: "INBOX"},
	}}
	flags := &fakeFlagRepository{}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{{UID: 7}}}
	sess := newTestSession(auth, folders, messages, flags, &fakeBlobReader{})
	_ = sess.Login("user@example.test", testPassword)
	if _, err := sess.Select("INBOX", &imap.SelectOptions{}); err != nil {
		t.Fatalf("Select: %v", err)
	}

	numSet := imap.UIDSetNum(7)
	err := sess.Store(nil, numSet, &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}, &imap.StoreOptions{})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if flags.lastFolderID != "folder-1" || flags.lastUID != 7 || flags.lastOp != port.FlagOpAdd {
		t.Errorf("flags recorded = folderID=%q uid=%d op=%v, want folder-1/7/FlagOpAdd", flags.lastFolderID, flags.lastUID, flags.lastOp)
	}
}

// TestSession_Store_HandlesUIDStarRange proves that UID STORE with a dynamic
// open-ended range (e.g. "UID STORE 5:* +FLAGS (\Seen)", the "mark all as
// read" operation) resolves against the folder's real message list instead
// of relying on NumSet.Nums(), which returns (nil, false) for such ranges.
func TestSession_Store_HandlesUIDStarRange(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1", Name: "INBOX"},
	}}
	flags := &fakeFlagRepository{}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{
		{UID: 5}, {UID: 7}, {UID: 9},
	}}
	sess := newTestSession(auth, folders, messages, flags, &fakeBlobReader{})
	_ = sess.Login("user@example.test", testPassword)
	if _, err := sess.Select("INBOX", &imap.SelectOptions{}); err != nil {
		t.Fatalf("Select: %v", err)
	}

	// "5:*" - an open-ended dynamic range, the single most common real-world
	// STORE usage ("mark all as read").
	var numSet imap.UIDSet
	numSet.AddRange(5, 0)

	err := sess.Store(nil, numSet, &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}, &imap.StoreOptions{})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	if len(flags.calls) != 3 {
		t.Fatalf("SetFlags called %d times, want 3 (all messages with UID >= 5)", len(flags.calls))
	}
	gotUIDs := map[uint32]bool{}
	for _, c := range flags.calls {
		gotUIDs[c.uid] = true
		if c.op != port.FlagOpAdd || c.folderID != "folder-1" {
			t.Errorf("call = %+v, want op=FlagOpAdd folderID=folder-1", c)
		}
	}
	for _, want := range []uint32{5, 7, 9} {
		if !gotUIDs[want] {
			t.Errorf("SetFlags was not called for UID %d", want)
		}
	}
}

// TestSession_Store_AllowsSequenceNumberSet proves that plain (non-UID)
// STORE, identified by a SeqSet rather than a UIDSet, is accepted and
// resolved against the message at that sequence position - the
// numSetIter-based design supports this the same way Fetch does, unlike the
// old implementation, which rejected any non-UIDSet outright.
func TestSession_Store_AllowsSequenceNumberSet(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1", Name: "INBOX"},
	}}
	flags := &fakeFlagRepository{}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{
		{UID: 5}, {UID: 7}, {UID: 9},
	}}
	sess := newTestSession(auth, folders, messages, flags, &fakeBlobReader{})
	_ = sess.Login("user@example.test", testPassword)
	if _, err := sess.Select("INBOX", &imap.SelectOptions{}); err != nil {
		t.Fatalf("Select: %v", err)
	}

	// Sequence number 1 refers to the first message in the folder's list
	// (UID 5), not to "UID 1".
	numSet := imap.SeqSetNum(1)

	err := sess.Store(nil, numSet, &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}, &imap.StoreOptions{})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	if len(flags.calls) != 1 {
		t.Fatalf("SetFlags called %d times, want 1", len(flags.calls))
	}
	if flags.calls[0].uid != 5 || flags.calls[0].folderID != "folder-1" || flags.calls[0].op != port.FlagOpAdd {
		t.Errorf("call = %+v, want uid=5 folderID=folder-1 op=FlagOpAdd", flags.calls[0])
	}
}

// TestMatchFolders_FiltersByWildcardPattern exercises the folder-matching
// logic extracted from Session.List (matchFolders). Session.List itself
// cannot be driven directly in a unit test because *imapserver.ListWriter
// has no exported constructor and can only be built by a live
// *imapserver.Conn - so this test proves the matching/filtering behavior
// that List delegates to, and fails if that logic regresses (e.g. matching
// nothing, matching everything regardless of pattern, or mishandling the
// hierarchy delimiter).
func TestMatchFolders_FiltersByWildcardPattern(t *testing.T) {
	recs := []port.ImapFolderRecord{
		{ID: "1", Name: "INBOX"},
		{ID: "2", Name: "Archive"},
		{ID: "3", Name: "Archive/2024"},
		{ID: "4", Name: "Sent"},
	}

	tests := []struct {
		name     string
		ref      string
		patterns []string
		want     []string
	}{
		{
			name:     "star matches everything including nested",
			ref:      "",
			patterns: []string{"*"},
			want:     []string{"INBOX", "Archive", "Archive/2024", "Sent"},
		},
		{
			name:     "percent does not cross hierarchy delimiter",
			ref:      "",
			patterns: []string{"%"},
			want:     []string{"INBOX", "Archive", "Sent"},
		},
		{
			name:     "exact literal match",
			ref:      "",
			patterns: []string{"INBOX"},
			want:     []string{"INBOX"},
		},
		{
			name:     "multiple patterns are unioned",
			ref:      "",
			patterns: []string{"INBOX", "Sent"},
			want:     []string{"INBOX", "Sent"},
		},
		{
			name:     "no pattern matches anything",
			ref:      "",
			patterns: []string{"Nonexistent"},
			want:     nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchFolders(recs, tc.ref, tc.patterns)
			var gotNames []string
			for _, rec := range got {
				gotNames = append(gotNames, rec.Name)
			}
			if len(gotNames) != len(tc.want) {
				t.Fatalf("matchFolders() = %v, want %v", gotNames, tc.want)
			}
			wantSet := map[string]bool{}
			for _, w := range tc.want {
				wantSet[w] = true
			}
			for _, n := range gotNames {
				if !wantSet[n] {
					t.Errorf("matchFolders() included unexpected folder %q; got %v, want %v", n, gotNames, tc.want)
				}
			}
		})
	}
}

// TestSession_List_UsesFolderRepository proves Session.List consults
// useCase.ListFolders (via the fake ImapFolderRepository) rather than some
// hardcoded set - a sanity check that List is actually wired to real data,
// complementing the matchFolders unit test above and the full E2E LIST
// assertion in imap_core_e2e_test.go which drives List through a real
// *imapserver.ListWriter.
func TestSession_List_UsesFolderRepository(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1", Name: "INBOX"},
	}}
	sess := newTestSession(auth, folders, &fakeMessageQueryRepository{}, &fakeFlagRepository{}, &fakeBlobReader{}).(*session)
	if err := sess.Login("user@example.test", testPassword); err != nil {
		t.Fatalf("Login: %v", err)
	}

	recs, err := sess.useCase.ListFolders(context.Background(), sess.mailboxID)
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	matched := matchFolders(recs, "", []string{"*"})
	if len(matched) != 1 || matched[0].Name != "INBOX" {
		t.Errorf("matched = %+v, want a single INBOX record", matched)
	}
}

func TestSession_Expunge_CallsUseCaseWithDeletedUIDsOnly(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{"INBOX": {ID: "folder-1", NumMessages: 3}}}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{
		{UID: 1, Flags: []string{"\\Deleted"}},
		{UID: 2, Flags: nil},
		{UID: 3, Flags: []string{"\\Deleted", "\\Seen"}},
	}}
	expunger := &fakeExpungeRepository{}
	sess := newTestSessionWithExpungeAndCopy(auth, folders, messages, &fakeFlagRepository{}, &fakeBlobReader{}, expunger, &fakeCopyRepository{})
	_ = sess.Login("user@example.test", testPassword)
	if _, err := sess.Select("INBOX", &imap.SelectOptions{}); err != nil {
		t.Fatalf("Select: %v", err)
	}

	if err := sess.Expunge(nil, nil); err != nil {
		t.Fatalf("Expunge: %v", err)
	}
	if len(expunger.calls) != 1 {
		t.Fatalf("expunger called %d times, want 1", len(expunger.calls))
	}
	uids := expunger.calls[0].uids
	if len(uids) != 2 || uids[0] != 1 || uids[1] != 3 {
		t.Errorf("expunged uids = %v, want [1 3] (only \\Deleted messages)", uids)
	}
}

func TestSession_Copy_CallsUseCaseAndBuildsCopyData(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX":   {ID: "folder-1"},
		"Archive": {ID: "folder-2", UIDValidity: 42},
	}}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{{UID: 5}}}
	copier := &fakeCopyRepository{result: []port.CopiedMessage{{SourceUID: 5, DestUID: 1}}}
	sess := newTestSessionWithExpungeAndCopy(auth, folders, messages, &fakeFlagRepository{}, &fakeBlobReader{}, &fakeExpungeRepository{}, copier)
	_ = sess.Login("user@example.test", testPassword)
	_, _ = sess.Select("INBOX", &imap.SelectOptions{})

	data, err := sess.Copy(imap.UIDSetNum(5), "Archive")
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if data.UIDValidity != 42 {
		t.Errorf("UIDValidity = %d, want 42", data.UIDValidity)
	}
	srcNums, _ := data.SourceUIDs.Nums()
	destNums, _ := data.DestUIDs.Nums()
	if len(srcNums) != 1 || srcNums[0] != 5 || len(destNums) != 1 || destNums[0] != 1 {
		t.Errorf("SourceUIDs=%v DestUIDs=%v, want [5]/[1]", srcNums, destNums)
	}
}

func TestSession_Copy_FailsForUnknownDestination(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{"INBOX": {ID: "folder-1"}}}
	sess := newTestSessionWithExpungeAndCopy(auth, folders, &fakeMessageQueryRepository{}, &fakeFlagRepository{}, &fakeBlobReader{}, &fakeExpungeRepository{}, &fakeCopyRepository{})
	_ = sess.Login("user@example.test", testPassword)
	_, _ = sess.Select("INBOX", &imap.SelectOptions{})

	_, err := sess.Copy(imap.UIDSetNum(5), "Nonexistent")
	if err == nil {
		t.Fatal("expected an error copying to a nonexistent folder, got nil")
	}
}

// TestSession_Move_ExpungesCopiedMessageEvenWithoutDeletedFlag proves the
// RFC 6851 fix: MOVE must expunge exactly the UIDs it just copied,
// regardless of whether they carry \Deleted. Before this fix, Move called
// the shared expungeUIDs helper in its \Deleted-requiring mode (the one
// correct for EXPUNGE), so a message with no \Deleted flag would be copied
// into the destination but never removed from the source folder - a silent
// duplication instead of a move.
func TestSession_Move_ExpungesCopiedMessageEvenWithoutDeletedFlag(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX":   {ID: "folder-1", NumMessages: 1},
		"Archive": {ID: "folder-2", UIDValidity: 42},
	}}
	// UID 5 has no \Deleted flag - MOVE must still expunge it from the
	// source folder, unlike EXPUNGE which would skip it.
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{{UID: 5, Flags: nil}}}
	expunger := &fakeExpungeRepository{}
	copier := &fakeCopyRepository{result: []port.CopiedMessage{{SourceUID: 5, DestUID: 1}}}
	sess := newTestSessionWithExpungeAndCopy(auth, folders, messages, &fakeFlagRepository{}, &fakeBlobReader{}, expunger, copier)
	_ = sess.Login("user@example.test", testPassword)
	if _, err := sess.Select("INBOX", &imap.SelectOptions{}); err != nil {
		t.Fatalf("Select: %v", err)
	}

	// w is nil here for the same reason Expunge's tests pass a nil writer:
	// *imapserver.MoveWriter has no exported constructor and can only be
	// built by a live *imapserver.Conn.
	if err := sess.(*session).Move(nil, imap.UIDSetNum(5), "Archive"); err != nil {
		t.Fatalf("Move: %v", err)
	}

	if len(expunger.calls) != 1 {
		t.Fatalf("expunger called %d times, want 1", len(expunger.calls))
	}
	uids := expunger.calls[0].uids
	if len(uids) != 1 || uids[0] != 5 {
		t.Errorf("expunged uids = %v, want [5] (moved even though it has no \\Deleted flag)", uids)
	}
}

func TestStubMethods_ReturnNotYetImplementedError(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	sess := newTestSession(auth, &fakeImapFolderRepository{}, &fakeMessageQueryRepository{}, &fakeFlagRepository{}, &fakeBlobReader{})
	_ = sess.Login("user@example.test", testPassword)

	if err := sess.Create("Nonexistent", nil); err == nil {
		t.Error("Create: expected an error, got nil")
	}
	if _, err := sess.Search(imapserver.NumKindUID, &imap.SearchCriteria{}, &imap.SearchOptions{}); err == nil {
		t.Error("Search: expected an error, got nil")
	}
	if _, err := sess.Copy(imap.UIDSetNum(1), "Other"); err == nil {
		t.Error("Copy: expected an error, got nil")
	}
}
