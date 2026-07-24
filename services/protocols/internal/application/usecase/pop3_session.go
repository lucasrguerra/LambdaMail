package usecase

import (
	"context"
	"fmt"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

type Pop3SessionUseCase struct {
	authRepo     port.AuthRepository
	folderRepo   port.ImapFolderRepository
	messageQuery port.MessageQueryRepository
	blobReader   port.BlobReader
	expungeRepo  port.ExpungeRepository
}

func NewPop3SessionUseCase(
	authRepo port.AuthRepository,
	folderRepo port.ImapFolderRepository,
	messageQuery port.MessageQueryRepository,
	blobReader port.BlobReader,
	expungeRepo port.ExpungeRepository,
) *Pop3SessionUseCase {
	return &Pop3SessionUseCase{
		authRepo:     authRepo,
		folderRepo:   folderRepo,
		messageQuery: messageQuery,
		blobReader:   blobReader,
		expungeRepo:  expungeRepo,
	}
}

func (uc *Pop3SessionUseCase) Login(ctx context.Context, username, password string) (string, error) {
	rec, err := uc.authRepo.FindByAddress(ctx, username)
	if err != nil {
		return "", fmt.Errorf("lookup mailbox %q: %w", username, err)
	}
	if rec == nil {
		return "", ErrAuthFailed
	}
	match, err := argon2id.ComparePasswordAndHash(password, rec.PasswordHash)
	if err != nil {
		return "", fmt.Errorf("verify password hash: %w", err)
	}
	if !match {
		return "", ErrAuthFailed
	}
	return rec.ID.String(), nil
}

func (uc *Pop3SessionUseCase) GetInbox(ctx context.Context, mailboxID string) (*port.ImapFolderRecord, []port.MessageRecord, error) {
	folder, err := uc.folderRepo.FindByName(ctx, mailboxID, "INBOX")
	if err != nil {
		return nil, nil, fmt.Errorf("find INBOX for mailbox %s: %w", mailboxID, err)
	}
	if folder == nil {
		return nil, nil, ErrNoSuchFolder
	}
	messages, err := uc.messageQuery.ListMessages(ctx, folder.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("list messages for INBOX folder %s: %w", folder.ID, err)
	}
	return folder, messages, nil
}

func (uc *Pop3SessionUseCase) ReadBlob(ctx context.Context, blobID uuid.UUID) ([]byte, error) {
	content, err := uc.blobReader.ReadByID(ctx, blobID)
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", blobID, err)
	}
	return content, nil
}

func (uc *Pop3SessionUseCase) ExpungeMessages(ctx context.Context, folderID string, uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	if err := uc.expungeRepo.Expunge(ctx, folderID, uids); err != nil {
		return fmt.Errorf("expunge messages in folder %s: %w", folderID, err)
	}
	return nil
}
