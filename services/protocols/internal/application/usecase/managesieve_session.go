package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/alexedwards/argon2id"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

var (
	ErrScriptNotFound      = errors.New("script does not exist")
	ErrActiveScriptDelete  = errors.New("cannot delete active script")
	ErrScriptAlreadyExists = errors.New("script with new name already exists")
	ErrScriptQuotaExceeded = errors.New("script size exceeds storage limit")
)

const MaxScriptSizeBytes = 1024 * 1024 // 1 MB limit per Sieve script

type ManageSieveSessionUseCase struct {
	authRepo  port.AuthRepository
	sieveRepo port.SieveRepository
}

func NewManageSieveSessionUseCase(authRepo port.AuthRepository, sieveRepo port.SieveRepository) *ManageSieveSessionUseCase {
	return &ManageSieveSessionUseCase{
		authRepo:  authRepo,
		sieveRepo: sieveRepo,
	}
}

func (uc *ManageSieveSessionUseCase) Login(ctx context.Context, address, password string) (string, error) {
	rec, err := uc.authRepo.FindByAddress(ctx, address)
	if err != nil {
		return "", fmt.Errorf("find mailbox: %w", err)
	}
	if rec == nil {
		return "", ErrAuthFailed
	}
	match, err := argon2id.ComparePasswordAndHash(password, rec.PasswordHash)
	if err != nil || !match {
		return "", ErrAuthFailed
	}
	return rec.ID.String(), nil
}

func (uc *ManageSieveSessionUseCase) PutScript(ctx context.Context, mailboxID, name, content string) error {
	if len(content) > MaxScriptSizeBytes {
		return ErrScriptQuotaExceeded
	}
	if err := entity.ValidateSieveScript(content); err != nil {
		return fmt.Errorf("invalid sieve script syntax: %w", err)
	}
	return uc.sieveRepo.PutScript(ctx, mailboxID, name, content)
}

func (uc *ManageSieveSessionUseCase) GetScript(ctx context.Context, mailboxID, name string) (*port.SieveScriptRecord, error) {
	rec, err := uc.sieveRepo.GetScript(ctx, mailboxID, name)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, ErrScriptNotFound
	}
	return rec, nil
}

func (uc *ManageSieveSessionUseCase) ListScripts(ctx context.Context, mailboxID string) ([]port.SieveScriptRecord, error) {
	return uc.sieveRepo.ListScripts(ctx, mailboxID)
}

func (uc *ManageSieveSessionUseCase) SetActive(ctx context.Context, mailboxID, name string) error {
	if name != "" {
		rec, err := uc.sieveRepo.GetScript(ctx, mailboxID, name)
		if err != nil {
			return err
		}
		if rec == nil {
			return ErrScriptNotFound
		}
	}
	return uc.sieveRepo.SetActiveScript(ctx, mailboxID, name)
}

func (uc *ManageSieveSessionUseCase) DeleteScript(ctx context.Context, mailboxID, name string) error {
	rec, err := uc.sieveRepo.GetScript(ctx, mailboxID, name)
	if err != nil {
		return err
	}
	if rec == nil {
		return ErrScriptNotFound
	}
	if rec.IsActive {
		return ErrActiveScriptDelete
	}
	return uc.sieveRepo.DeleteScript(ctx, mailboxID, name)
}

func (uc *ManageSieveSessionUseCase) RenameScript(ctx context.Context, mailboxID, oldName, newName string) error {
	oldRec, err := uc.sieveRepo.GetScript(ctx, mailboxID, oldName)
	if err != nil {
		return err
	}
	if oldRec == nil {
		return ErrScriptNotFound
	}

	newRec, err := uc.sieveRepo.GetScript(ctx, mailboxID, newName)
	if err != nil {
		return err
	}
	if newRec != nil {
		return ErrScriptAlreadyExists
	}

	return uc.sieveRepo.RenameScript(ctx, mailboxID, oldName, newName)
}

func (uc *ManageSieveSessionUseCase) CheckScript(_ context.Context, content string) error {
	if len(content) > MaxScriptSizeBytes {
		return ErrScriptQuotaExceeded
	}
	return entity.ValidateSieveScript(content)
}
