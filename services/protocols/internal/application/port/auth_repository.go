package port

import (
	"context"

	"github.com/google/uuid"
)

// MailboxAuth is the subset of mailbox data needed to verify a login and,
// once authenticated, to apply the sender policy of PLAN.md section 5.2.
type MailboxAuth struct {
	ID           uuid.UUID
	PasswordHash string
	EmailAddress string
	DomainName   string
	// MaxRecipientsPerHour caps a compromised account's blast radius.
	MaxRecipientsPerHour int
	// AppPasswords are the per-client credentials the user generated in the
	// webmail. Protocol clients cannot answer a second factor, so these are how
	// a mail client signs in on an account that has one.
	AppPasswords []AppPasswordAuth
}

// AppPasswordAuth is one app password as stored: a hash, never the secret.
type AppPasswordAuth struct {
	ID     uuid.UUID
	Hash   string
	Scopes []string
}

// AppPasswordUseRecorder is implemented by repositories that can note when an
// app password was last used. It is optional: a fake in a test need not have it.
type AppPasswordUseRecorder interface {
	RecordAppPasswordUse(ctx context.Context, id uuid.UUID) error
}

// MaxAppPasswordsChecked bounds how many app passwords one login attempt is
// compared against.
const MaxAppPasswordsChecked = 20

// AuthRepository resolves a login address to its stored credential.
type AuthRepository interface {
	// FindByAddress returns nil, nil (not an error) when no active mailbox
	// with an active domain matches address.
	FindByAddress(ctx context.Context, address string) (*MailboxAuth, error)
}
