package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// Light parameters: the tests check logic, not the cost of a real hash.
var testArgon = &argon2id.Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func mustHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := argon2id.CreateHash(password, testArgon)
	if err != nil {
		t.Fatalf("create hash: %v", err)
	}
	return hash
}

// recordingAuthRepo is an AuthRepository that also records which app password
// was used, like the Postgres one does.
type recordingAuthRepo struct {
	used []uuid.UUID
}

func (r *recordingAuthRepo) FindByAddress(context.Context, string) (*port.MailboxAuth, error) {
	return nil, nil
}

func (r *recordingAuthRepo) RecordAppPasswordUse(_ context.Context, id uuid.UUID) error {
	r.used = append(r.used, id)
	return nil
}

func TestVerifyCredential(t *testing.T) {
	appID := uuid.New()
	rec := &port.MailboxAuth{
		ID:           uuid.New(),
		PasswordHash: mustHash(t, "main-password-123"),
		AppPasswords: []port.AppPasswordAuth{
			{ID: appID, Hash: mustHash(t, "app-password-abc"), Scopes: []string{ScopeSMTP, ScopeIMAP}},
			{ID: uuid.New(), Hash: mustHash(t, "imap-only-xyz"), Scopes: []string{ScopeIMAP}},
		},
	}
	repo := &recordingAuthRepo{}

	cases := []struct {
		name     string
		password string
		scope    string
		want     bool
	}{
		{"main password works for smtp", "main-password-123", ScopeSMTP, true},
		{"main password works for imap", "main-password-123", ScopeIMAP, true},
		{"app password works in a scope it holds", "app-password-abc", ScopeSMTP, true},
		{"app password works for imap too", "app-password-abc", ScopeIMAP, true},
		{"app password is refused outside its scopes", "app-password-abc", ScopePOP3, false},
		{"an imap-only app password cannot send", "imap-only-xyz", ScopeSMTP, false},
		{"an imap-only app password can read", "imap-only-xyz", ScopeIMAP, true},
		{"wrong password fails", "nope", ScopeSMTP, false},
		{"empty password fails", "", ScopeSMTP, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := verifyCredential(context.Background(), repo, rec, tc.password, tc.scope)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("verifyCredential = %v, want %v", got, tc.want)
			}
		})
	}

	// Only the successful app password logins were recorded, and only that one
	for _, id := range repo.used {
		if id == rec.ID {
			t.Fatalf("recorded the mailbox id as an app password use")
		}
	}
	if len(repo.used) == 0 {
		t.Fatalf("expected app password use to be recorded")
	}
}

func TestVerifyCredentialMainPasswordDoesNotRecordAppUse(t *testing.T) {
	rec := &port.MailboxAuth{
		ID:           uuid.New(),
		PasswordHash: mustHash(t, "main-password-123"),
		AppPasswords: []port.AppPasswordAuth{{ID: uuid.New(), Hash: mustHash(t, "app-password-abc"), Scopes: []string{ScopeSMTP}}},
	}
	repo := &recordingAuthRepo{}

	ok, err := verifyCredential(context.Background(), repo, rec, "main-password-123", ScopeSMTP)
	if err != nil || !ok {
		t.Fatalf("main password: ok=%v err=%v", ok, err)
	}
	if len(repo.used) != 0 {
		t.Fatalf("a main password login must not touch app passwords, recorded %v", repo.used)
	}
}

func TestVerifyCredentialMalformedAppHashIsSkipped(t *testing.T) {
	rec := &port.MailboxAuth{
		ID:           uuid.New(),
		PasswordHash: mustHash(t, "main-password-123"),
		AppPasswords: []port.AppPasswordAuth{
			{ID: uuid.New(), Hash: "not-a-phc-string", Scopes: []string{ScopeSMTP}},
			{ID: uuid.New(), Hash: mustHash(t, "app-password-abc"), Scopes: []string{ScopeSMTP}},
		},
	}

	ok, err := verifyCredential(context.Background(), &recordingAuthRepo{}, rec, "app-password-abc", ScopeSMTP)
	if err != nil || !ok {
		t.Fatalf("a good app password must still work next to a broken one: ok=%v err=%v", ok, err)
	}

	ok, err = verifyCredential(context.Background(), &recordingAuthRepo{}, rec, "wrong", ScopeSMTP)
	if err != nil || ok {
		t.Fatalf("a broken app hash must not turn a wrong password into an error: ok=%v err=%v", ok, err)
	}
}

func TestVerifyCredentialMalformedMainHashKeepsReportingTheError(t *testing.T) {
	rec := &port.MailboxAuth{ID: uuid.New(), PasswordHash: "garbage"}

	ok, err := verifyCredential(context.Background(), &recordingAuthRepo{}, rec, "anything", ScopeSMTP)
	if ok {
		t.Fatalf("must not authenticate against a malformed hash")
	}
	if err == nil {
		t.Fatalf("a malformed main hash is a server-side fault and must surface as an error, as before")
	}
}

func TestVerifyCredentialMalformedMainHashStillAllowsAnAppPassword(t *testing.T) {
	rec := &port.MailboxAuth{
		ID:           uuid.New(),
		PasswordHash: "garbage",
		AppPasswords: []port.AppPasswordAuth{{ID: uuid.New(), Hash: mustHash(t, "app-password-abc"), Scopes: []string{ScopeSMTP}}},
	}

	ok, err := verifyCredential(context.Background(), &recordingAuthRepo{}, rec, "app-password-abc", ScopeSMTP)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

// The three protocol entry points, wired the way the servers wire them: an app
// password signs in where its scopes allow, and only there.

func appPasswordMailbox(t *testing.T, scopes ...string) (port.MailboxAuth, string) {
	t.Helper()
	const secret = "app-password-from-the-webmail"
	return port.MailboxAuth{
		ID:           uuid.New(),
		PasswordHash: mustHash(t, "the-real-mailbox-password"),
		AppPasswords: []port.AppPasswordAuth{{ID: uuid.New(), Hash: mustHash(t, secret), Scopes: scopes}},
	}, secret
}

func TestSubmissionAuthenticate_AcceptsAnAppPasswordWithTheSmtpScope(t *testing.T) {
	rec, secret := appPasswordMailbox(t, ScopeSMTP)
	uc := NewProcessOutboundEmailUseCase(&fakeAuthRepo{record: &rec}, nil, nil, nil)

	got, err := uc.Authenticate(context.Background(), "user@example.test", secret)
	if err != nil || got == nil || got.ID != rec.ID {
		t.Fatalf("Authenticate = %+v, %v; want the mailbox", got, err)
	}
}

func TestSubmissionAuthenticate_RefusesAnAppPasswordWithoutTheSmtpScope(t *testing.T) {
	rec, secret := appPasswordMailbox(t, ScopeIMAP)
	uc := NewProcessOutboundEmailUseCase(&fakeAuthRepo{record: &rec}, nil, nil, nil)

	if _, err := uc.Authenticate(context.Background(), "user@example.test", secret); !errors.Is(err, ErrSubmissionAuthFailed) {
		t.Fatalf("error = %v, want ErrSubmissionAuthFailed", err)
	}
}

func TestImapLogin_AcceptsAnAppPasswordWithTheImapScope(t *testing.T) {
	rec, secret := appPasswordMailbox(t, ScopeIMAP)
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{"user@example.test": rec}}
	uc := NewImapSessionUseCase(auth, nil, nil, nil, nil, nil, nil)

	id, err := uc.Login(context.Background(), "user@example.test", secret)
	if err != nil || id != rec.ID.String() {
		t.Fatalf("Login = %q, %v; want %q", id, err, rec.ID)
	}
}

func TestImapLogin_RefusesAnAppPasswordWithoutTheImapScope(t *testing.T) {
	rec, secret := appPasswordMailbox(t, ScopeSMTP)
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{"user@example.test": rec}}
	uc := NewImapSessionUseCase(auth, nil, nil, nil, nil, nil, nil)

	if _, err := uc.Login(context.Background(), "user@example.test", secret); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("error = %v, want ErrAuthFailed", err)
	}
}

func TestPop3Login_AcceptsAnAppPasswordWithThePop3Scope(t *testing.T) {
	rec, secret := appPasswordMailbox(t, ScopePOP3)
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{"user@example.test": rec}}
	uc := NewPop3SessionUseCase(auth, nil, nil, nil, nil)

	id, err := uc.Login(context.Background(), "user@example.test", secret)
	if err != nil || id != rec.ID.String() {
		t.Fatalf("Login = %q, %v; want %q", id, err, rec.ID)
	}
}
