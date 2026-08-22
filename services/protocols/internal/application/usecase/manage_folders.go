package usecase

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"lambdamail/protocols/internal/application/port"
)

// ManageFoldersUseCase lets a mailbox owner keep folders of their own.
//
// Only the six standard folders existed, so a rule that said "file this in
// Faturas" named a folder there was no way to create.
type ManageFoldersUseCase struct {
	folders port.FolderAdmin
	webmail port.WebmailRepository
}

func NewManageFoldersUseCase(folders port.FolderAdmin, webmail port.WebmailRepository) *ManageFoldersUseCase {
	return &ManageFoldersUseCase{folders: folders, webmail: webmail}
}

var (
	ErrInvalidFolderName = errors.New("folders: that name cannot be used")
	ErrFolderExists      = errors.New("folders: a folder with that name already exists")
	ErrFolderReserved    = errors.New("folders: that folder belongs to the mail system")
	ErrFolderNotFound    = errors.New("folders: no folder with that name")
)

// reservedFolders may not be created, renamed or deleted by a user.
//
// Each one means something to the protocol or to this server: INBOX is where
// delivery lands, Trash is where deleting puts things, Drafts and Sent record
// what a message is, and Reports is filled by the report ingestion. Letting a
// user rename one would break those paths in ways no error message explains.
var reservedFolders = map[string]bool{
	"inbox": true, "sent": true, "drafts": true,
	"trash": true, "junk": true, "archive": true, "reports": true,
}

// maxFolderNameLength keeps a name inside the column and inside what an IMAP
// client will show.
const maxFolderNameLength = 100

func (uc *ManageFoldersUseCase) Create(ctx context.Context, address, name string) error {
	name = strings.TrimSpace(name)
	if err := validateFolderName(name); err != nil {
		return err
	}

	mailboxID, err := uc.mailbox(ctx, address)
	if err != nil {
		return err
	}
	existing, err := uc.folders.ListFolders(ctx, mailboxID)
	if err != nil {
		return err
	}
	if findFolder(existing, name) != nil {
		return ErrFolderExists
	}
	return uc.folders.CreateFolder(ctx, mailboxID, name)
}

func (uc *ManageFoldersUseCase) Rename(ctx context.Context, address, from, to string) error {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if reservedFolders[strings.ToLower(from)] {
		return ErrFolderReserved
	}
	if err := validateFolderName(to); err != nil {
		return err
	}

	mailboxID, err := uc.mailbox(ctx, address)
	if err != nil {
		return err
	}
	existing, err := uc.folders.ListFolders(ctx, mailboxID)
	if err != nil {
		return err
	}
	if findFolder(existing, from) == nil {
		return ErrFolderNotFound
	}
	if findFolder(existing, to) != nil {
		return ErrFolderExists
	}
	return uc.folders.RenameFolder(ctx, mailboxID, from, to)
}

// Delete removes a folder the user created.
//
// The messages inside it go with it, which is why the interface has to say so
// before calling this.
func (uc *ManageFoldersUseCase) Delete(ctx context.Context, address, name string) error {
	name = strings.TrimSpace(name)
	if reservedFolders[strings.ToLower(name)] {
		return ErrFolderReserved
	}

	mailboxID, err := uc.mailbox(ctx, address)
	if err != nil {
		return err
	}
	existing, err := uc.folders.ListFolders(ctx, mailboxID)
	if err != nil {
		return err
	}
	if findFolder(existing, name) == nil {
		return ErrFolderNotFound
	}
	return uc.folders.DeleteFolder(ctx, mailboxID, name)
}

func (uc *ManageFoldersUseCase) mailbox(ctx context.Context, address string) (string, error) {
	id, err := uc.webmail.FindMailboxIDByAddress(ctx, address)
	if err != nil || id == "" {
		return "", ErrNoSuchMailbox
	}
	return id, nil
}

func findFolder(folders []port.WebmailFolder, name string) *port.WebmailFolder {
	for i, folder := range folders {
		if strings.EqualFold(strings.TrimSpace(folder.Name), name) {
			return &folders[i]
		}
	}
	return nil
}

// validateFolderName rejects names that would confuse IMAP or the routing.
func validateFolderName(name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrInvalidFolderName
	}
	if utf8.RuneCountInString(name) > maxFolderNameLength {
		return ErrInvalidFolderName
	}
	if reservedFolders[strings.ToLower(name)] {
		return ErrFolderReserved
	}
	// A delimiter would read as a nested folder to an IMAP client, and this
	// server does not create hierarchies.
	if strings.ContainsAny(name, `/\.`) {
		return ErrInvalidFolderName
	}
	// Control characters have no place in a name that travels over IMAP.
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return ErrInvalidFolderName
		}
	}
	return nil
}
