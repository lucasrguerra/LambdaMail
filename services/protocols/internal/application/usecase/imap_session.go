package usecase

import (
	"context"
	"errors"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// ErrAuthFailed is returned by Login for any credential problem - unknown
// address, wrong password, or (once AuthRepository filters it) an inactive
// mailbox/domain. Deliberately the same error in every case: distinguishing
// them would let a client enumerate valid addresses (PLAN.md section 14.6).
var ErrAuthFailed = errors.New("imap session: authentication failed")

// ErrNoSuchFolder is returned by SelectFolder when the named folder doesn't exist.
var ErrNoSuchFolder = errors.New("imap session: no such folder")

// ImapSessionUseCase implements the read/auth half of PLAN.md section 3's
// HandleImapCommandUseCase (this sub-project's scope: LOGIN, LIST, SELECT,
// FETCH, STORE; SEARCH/EXPUNGE/IDLE/MOVE land in later sub-projects).
type ImapSessionUseCase struct {
	auth     port.AuthRepository
	folders  port.ImapFolderRepository
	messages port.MessageQueryRepository
	flags    port.FlagRepository
	blobs    port.BlobReader
	expunger port.ExpungeRepository
	copier   port.CopyRepository
}

func NewImapSessionUseCase(auth port.AuthRepository, folders port.ImapFolderRepository, messages port.MessageQueryRepository, flags port.FlagRepository, blobs port.BlobReader, expunger port.ExpungeRepository, copier port.CopyRepository) *ImapSessionUseCase {
	return &ImapSessionUseCase{auth: auth, folders: folders, messages: messages, flags: flags, blobs: blobs, expunger: expunger, copier: copier}
}

func (uc *ImapSessionUseCase) Login(ctx context.Context, address, password string) (string, error) {
	rec, err := uc.auth.FindByAddress(ctx, address)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", ErrAuthFailed
	}
	match, err := argon2id.ComparePasswordAndHash(password, rec.PasswordHash)
	if err != nil {
		return "", err
	}
	if !match {
		return "", ErrAuthFailed
	}
	return rec.ID.String(), nil
}

func (uc *ImapSessionUseCase) ListFolders(ctx context.Context, mailboxID string) ([]port.ImapFolderRecord, error) {
	return uc.folders.ListFolders(ctx, mailboxID)
}

func (uc *ImapSessionUseCase) SelectFolder(ctx context.Context, mailboxID, name string) (*port.ImapFolderRecord, error) {
	rec, err := uc.folders.FindByName(ctx, mailboxID, name)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, ErrNoSuchFolder
	}
	return rec, nil
}

func (uc *ImapSessionUseCase) FetchMessages(ctx context.Context, folderID string) ([]port.MessageRecord, error) {
	return uc.messages.ListMessages(ctx, folderID)
}

func (uc *ImapSessionUseCase) ReadBlob(ctx context.Context, blobID uuid.UUID) ([]byte, error) {
	return uc.blobs.ReadByID(ctx, blobID)
}

func (uc *ImapSessionUseCase) SetFlags(ctx context.Context, folderID string, uid uint32, op port.FlagOp, flags []string) error {
	return uc.flags.SetFlags(ctx, folderID, uid, op, flags)
}

func (uc *ImapSessionUseCase) Expunge(ctx context.Context, folderID string, uids []uint32) error {
	return uc.expunger.Expunge(ctx, folderID, uids)
}

func (uc *ImapSessionUseCase) CopyMessages(ctx context.Context, sourceFolderID string, uids []uint32, destFolderID string) ([]port.CopiedMessage, error) {
	return uc.copier.CopyMessages(ctx, sourceFolderID, uids, destFolderID)
}
