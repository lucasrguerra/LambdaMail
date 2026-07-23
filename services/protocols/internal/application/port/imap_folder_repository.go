package port

import "context"

// ImapFolderRecord is the IMAP-relevant subset of a folder row.
type ImapFolderRecord struct {
	ID          string
	Name        string
	UIDNext     uint32
	UIDValidity uint32
	NumMessages uint32
}

// ImapFolderRepository resolves a mailbox's folders for LIST and SELECT.
type ImapFolderRepository interface {
	ListFolders(ctx context.Context, mailboxID string) ([]ImapFolderRecord, error)
	// FindByName returns nil, nil (not an error) when no folder with that
	// name exists for the mailbox.
	FindByName(ctx context.Context, mailboxID, name string) (*ImapFolderRecord, error)
}
