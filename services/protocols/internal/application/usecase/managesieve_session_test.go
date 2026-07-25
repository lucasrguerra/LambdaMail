package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"lambdamail/protocols/internal/application/port"
)

type fakeSieveRepository struct {
	scripts map[string]map[string]port.SieveScriptRecord // mailboxID -> name -> record
}

func newFakeSieveRepository() *fakeSieveRepository {
	return &fakeSieveRepository{
		scripts: make(map[string]map[string]port.SieveScriptRecord),
	}
}

func (f *fakeSieveRepository) GetScript(_ context.Context, mailboxID, name string) (*port.SieveScriptRecord, error) {
	mb, ok := f.scripts[mailboxID]
	if !ok {
		return nil, nil
	}
	rec, ok := mb[name]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

func (f *fakeSieveRepository) PutScript(_ context.Context, mailboxID, name, script string) error {
	mb, ok := f.scripts[mailboxID]
	if !ok {
		mb = make(map[string]port.SieveScriptRecord)
		f.scripts[mailboxID] = mb
	}
	existing, exists := mb[name]
	isActive := false
	if exists {
		isActive = existing.IsActive
	}
	mb[name] = port.SieveScriptRecord{
		ID:        uuid.New().String(),
		MailboxID: mailboxID,
		Name:      name,
		Script:    script,
		IsActive:  isActive,
	}
	return nil
}

func (f *fakeSieveRepository) ListScripts(_ context.Context, mailboxID string) ([]port.SieveScriptRecord, error) {
	mb, ok := f.scripts[mailboxID]
	if !ok {
		return nil, nil
	}
	var out []port.SieveScriptRecord
	for _, rec := range mb {
		out = append(out, rec)
	}
	return out, nil
}

func (f *fakeSieveRepository) SetActiveScript(_ context.Context, mailboxID, name string) error {
	mb, ok := f.scripts[mailboxID]
	if !ok {
		return nil
	}
	for k, rec := range mb {
		rec.IsActive = (k == name)
		mb[k] = rec
	}
	return nil
}

func (f *fakeSieveRepository) DeleteScript(_ context.Context, mailboxID, name string) error {
	mb, ok := f.scripts[mailboxID]
	if !ok {
		return nil
	}
	delete(mb, name)
	return nil
}

func (f *fakeSieveRepository) RenameScript(_ context.Context, mailboxID, oldName, newName string) error {
	mb, ok := f.scripts[mailboxID]
	if !ok {
		return nil
	}
	rec := mb[oldName]
	delete(mb, oldName)
	rec.Name = newName
	mb[newName] = rec
	return nil
}

func TestManageSieveSessionUseCase_Put_Get_List_SetActive_Delete(t *testing.T) {
	mailboxID := uuid.New()
	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: testPasswordHash},
	}}
	sieveRepo := newFakeSieveRepository()
	uc := NewManageSieveSessionUseCase(auth, sieveRepo)

	ctx := context.Background()

	// 1. Login
	gotID, err := uc.Login(ctx, "user@example.test", testPassword)
	if err != nil || gotID != mailboxID.String() {
		t.Fatalf("Login failed: err=%v, gotID=%s", err, gotID)
	}

	// 2. PutScript valid
	scriptContent := `require ["fileinto"]; if header :is "Subject" "Spam" { fileinto "Junk"; }`
	if err := uc.PutScript(ctx, gotID, "main_rules", scriptContent); err != nil {
		t.Fatalf("PutScript failed: %v", err)
	}

	// 3. PutScript invalid syntax
	if err := uc.PutScript(ctx, gotID, "bad_rules", `if header "Subject" {`); err == nil {
		t.Fatal("expected error for invalid Sieve syntax, got nil")
	}

	// 4. GetScript
	rec, err := uc.GetScript(ctx, gotID, "main_rules")
	if err != nil || rec == nil || rec.Script != scriptContent {
		t.Fatalf("GetScript failed: rec=%+v, err=%v", rec, err)
	}

	// 5. SetActive
	if err := uc.SetActive(ctx, gotID, "main_rules"); err != nil {
		t.Fatalf("SetActive failed: %v", err)
	}

	// 6. Delete active script fails per RFC 5804 §2.7
	if err := uc.DeleteScript(ctx, gotID, "main_rules"); !errors.Is(err, ErrActiveScriptDelete) {
		t.Fatalf("expected ErrActiveScriptDelete, got %v", err)
	}

	// 7. Deactivate all and delete
	if err := uc.SetActive(ctx, gotID, ""); err != nil {
		t.Fatalf("SetActive empty failed: %v", err)
	}
	if err := uc.DeleteScript(ctx, gotID, "main_rules"); err != nil {
		t.Fatalf("DeleteScript failed: %v", err)
	}
}
