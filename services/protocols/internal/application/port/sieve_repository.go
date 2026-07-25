package port

import "context"

// SieveScriptRecord represents a persistent Sieve script row in PostgreSQL.
type SieveScriptRecord struct {
	ID        string
	MailboxID string
	Name      string
	Script    string
	IsActive  bool
}

// SieveRepository defines data operations for Sieve script management.
type SieveRepository interface {
	GetScript(ctx context.Context, mailboxID string, name string) (*SieveScriptRecord, error)
	PutScript(ctx context.Context, mailboxID string, name string, script string) error
	ListScripts(ctx context.Context, mailboxID string) ([]SieveScriptRecord, error)
	SetActiveScript(ctx context.Context, mailboxID string, name string) error
	DeleteScript(ctx context.Context, mailboxID string, name string) error
	RenameScript(ctx context.Context, mailboxID string, oldName string, newName string) error
}
