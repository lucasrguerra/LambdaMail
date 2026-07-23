package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/application/port"
)

const (
	testPassword     = "correct horse battery staple"
	testPasswordHash = "$argon2id$v=19$m=65536,t=1,p=12$I88aUc0rZHHX+Bt+jX+4Ag$3jKTtg1khJjFo9az4iPvFLc3eKY8WKSFy/1YMXlKvSU"
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

func TestImapSessionUseCase_Login_SucceedsForCorrectPassword(t *testing.T) {
	mailboxID := uuid.New()
	// PHC hash for password "correct horse battery staple", generated with argon2id.CreateHash.
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	uc := NewImapSessionUseCase(auth, nil, nil, nil, nil)

	gotID, err := uc.Login(context.Background(), "user@example.test", testPassword)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotID != mailboxID.String() {
		t.Errorf("mailboxID = %q, want %q", gotID, mailboxID.String())
	}
}

func TestImapSessionUseCase_Login_FailsForWrongPassword(t *testing.T) {
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: uuid.New(), PasswordHash: testPasswordHash},
	}}
	uc := NewImapSessionUseCase(auth, nil, nil, nil, nil)

	_, err := uc.Login(context.Background(), "user@example.test", "wrong-password")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("error = %v, want ErrAuthFailed", err)
	}
}

func TestImapSessionUseCase_Login_FailsForUnknownAddress(t *testing.T) {
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{}}
	uc := NewImapSessionUseCase(auth, nil, nil, nil, nil)

	_, err := uc.Login(context.Background(), "nobody@example.test", "anything")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("error = %v, want ErrAuthFailed (never a distinct \"no such user\" error - PLAN.md section 14.6)", err)
	}
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

func TestImapSessionUseCase_SelectFolder_ReturnsRecordForExistingFolder(t *testing.T) {
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1", Name: "INBOX", UIDNext: 3, UIDValidity: 1000, NumMessages: 2},
	}}
	uc := NewImapSessionUseCase(nil, folders, nil, nil, nil)

	rec, err := uc.SelectFolder(context.Background(), "mailbox-1", "INBOX")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.ID != "folder-1" || rec.UIDNext != 3 {
		t.Errorf("rec = %+v, want folder-1 with UIDNext 3", rec)
	}
}

func TestImapSessionUseCase_SelectFolder_ReturnsErrNoSuchFolderWhenMissing(t *testing.T) {
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{}}
	uc := NewImapSessionUseCase(nil, folders, nil, nil, nil)

	_, err := uc.SelectFolder(context.Background(), "mailbox-1", "Nonexistent")
	if !errors.Is(err, ErrNoSuchFolder) {
		t.Fatalf("error = %v, want ErrNoSuchFolder", err)
	}
}
