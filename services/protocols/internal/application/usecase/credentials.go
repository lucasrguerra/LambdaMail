package usecase

import (
	"context"
	"log/slog"

	"github.com/alexedwards/argon2id"

	"lambdamail/protocols/internal/application/port"
)

// Scopes an app password can be limited to. They match the values of
// app_passwords.scopes, whose default is {imap,pop3,smtp}.
const (
	ScopeSMTP = "smtp"
	ScopeIMAP = "imap"
	ScopePOP3 = "pop3"
)

// verifyCredential reports whether password unlocks the mailbox for a client
// of the given protocol: either the mailbox's own password, or one of its app
// passwords that holds the scope.
//
// Before this, every protocol compared only against the mailbox password, so
// an app password - created in the webmail precisely because a mail client
// cannot answer a second factor - never worked anywhere.
//
// An error is returned only when the mailbox's own stored hash is unreadable
// (a server-side fault, reported as before) and no app password matched. A
// broken app password hash is skipped: it must not take the good ones, or a
// plain wrong-password answer, down with it.
func verifyCredential(ctx context.Context, repo port.AuthRepository, rec *port.MailboxAuth, password, scope string) (bool, error) {
	match, mainErr := argon2id.ComparePasswordAndHash(password, rec.PasswordHash)
	if mainErr == nil && match {
		return true, nil
	}

	for _, app := range rec.AppPasswords {
		if !hasScope(app.Scopes, scope) {
			continue
		}
		ok, err := argon2id.ComparePasswordAndHash(password, app.Hash)
		if err != nil || !ok {
			continue
		}
		if recorder, can := repo.(port.AppPasswordUseRecorder); can {
			// Best effort: a failed timestamp must not fail a login that succeeded
			if err := recorder.RecordAppPasswordUse(ctx, app.ID); err != nil {
				slog.Warn("record app password use", "error", err)
			}
		}
		return true, nil
	}

	if mainErr != nil {
		return false, mainErr
	}
	return false, nil
}

func hasScope(scopes []string, scope string) bool {
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}
