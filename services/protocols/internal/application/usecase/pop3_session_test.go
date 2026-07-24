package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/application/port"
)

func TestPop3SessionUseCase_Login_SucceedsForCorrectPassword(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	uc := NewPop3SessionUseCase(auth, nil, nil, nil, nil)

	gotID, err := uc.Login(context.Background(), "user@example.test", testPassword)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotID != mailboxID.String() {
		t.Errorf("mailboxID = %q, want %q", gotID, mailboxID.String())
	}
}

func TestPop3SessionUseCase_Login_FailsForWrongPassword(t *testing.T) {
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: uuid.New(), PasswordHash: testPasswordHash},
	}}
	uc := NewPop3SessionUseCase(auth, nil, nil, nil, nil)

	_, err := uc.Login(context.Background(), "user@example.test", "wrong-password")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("error = %v, want ErrAuthFailed", err)
	}
}

type fakeMessageQueryRepository struct {
	messages []port.MessageRecord
}

func (f *fakeMessageQueryRepository) ListMessages(_ context.Context, _ string) ([]port.MessageRecord, error) {
	return f.messages, nil
}

func TestPop3SessionUseCase_GetInbox_ReturnsFolderAndMessages(t *testing.T) {
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1", Name: "INBOX", NumMessages: 1},
	}}
	messages := &fakeMessageQueryRepository{messages: []port.MessageRecord{
		{UID: 1, SizeBytes: 100},
	}}
	uc := NewPop3SessionUseCase(nil, folders, messages, nil, nil)

	folder, recs, err := uc.GetInbox(context.Background(), "mailbox-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if folder.ID != "folder-1" || len(recs) != 1 || recs[0].UID != 1 {
		t.Errorf("unexpected inbox result: folder=%+v, recs=%+v", folder, recs)
	}
}

func TestPop3SessionUseCase_ExpungeMessages_DelegatesToRepo(t *testing.T) {
	expunger := &fakeExpungeRepository{}
	uc := NewPop3SessionUseCase(nil, nil, nil, nil, expunger)

	if err := uc.ExpungeMessages(context.Background(), "folder-1", []uint32{1, 2}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(expunger.calls) != 1 || expunger.calls[0].folderID != "folder-1" {
		t.Fatalf("unexpected expunger calls: %+v", expunger.calls)
	}
}
