package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
)

// Folders the mailbox owner creates for themselves.
//
// The six standard folders were all a mailbox could ever have, so a rule that
// said "file this in Faturas" pointed at a folder that could not be brought
// into existence from anywhere in the interface.

type stubFolderStore struct {
	existing []port.WebmailFolder
	created  []string
	renamed  []string
	deleted  []string
	err      error
}

func (s *stubFolderStore) ListFolders(context.Context, string) ([]port.WebmailFolder, error) {
	return s.existing, s.err
}

func (s *stubFolderStore) CreateFolder(_ context.Context, _, name string) error {
	if s.err != nil {
		return s.err
	}
	s.created = append(s.created, name)
	s.existing = append(s.existing, port.WebmailFolder{Name: name})
	return nil
}

func (s *stubFolderStore) RenameFolder(_ context.Context, _, from, to string) error {
	if s.err != nil {
		return s.err
	}
	s.renamed = append(s.renamed, from+"->"+to)
	return nil
}

func (s *stubFolderStore) DeleteFolder(_ context.Context, _, name string) error {
	if s.err != nil {
		return s.err
	}
	s.deleted = append(s.deleted, name)
	return nil
}

func newFolders(store *stubFolderStore) *ManageFoldersUseCase {
	return NewManageFoldersUseCase(store, &stubWebmailRepo{mailboxID: uuid.NewString()})
}

func standardFolders() []port.WebmailFolder {
	return []port.WebmailFolder{
		{Name: "INBOX", SpecialUse: "inbox"},
		{Name: "Sent", SpecialUse: "sent"},
		{Name: "Drafts", SpecialUse: "drafts"},
		{Name: "Trash", SpecialUse: "trash"},
	}
}

func TestCreatesAFolder(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	if err := newFolders(store).Create(context.Background(), "me@example.test", "Faturas"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(store.created) != 1 || store.created[0] != "Faturas" {
		t.Errorf("created %v", store.created)
	}
}

func TestFolderNameIsTrimmed(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	if err := newFolders(store).Create(context.Background(), "me@example.test", "  Faturas  "); err != nil {
		t.Fatalf("create: %v", err)
	}
	if store.created[0] != "Faturas" {
		t.Errorf("stored %q with its surrounding spaces", store.created[0])
	}
}

func TestRefusesAnEmptyName(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	for _, name := range []string{"", "   ", "\t"} {
		if err := newFolders(store).Create(context.Background(), "me@example.test", name); !errors.Is(err, ErrInvalidFolderName) {
			t.Errorf("creating %q returned %v", name, err)
		}
	}
}

// A second folder with the same name would make "file this in Faturas"
// ambiguous, and IMAP addresses folders by name.
func TestRefusesADuplicateName(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	if err := newFolders(store).Create(context.Background(), "me@example.test", "Faturas"); err != nil {
		t.Fatal(err)
	}
	if err := newFolders(store).Create(context.Background(), "me@example.test", "Faturas"); !errors.Is(err, ErrFolderExists) {
		t.Errorf("a duplicate name returned %v", err)
	}
}

func TestDuplicateCheckIgnoresCase(t *testing.T) {
	store := &stubFolderStore{existing: append(standardFolders(), port.WebmailFolder{Name: "Faturas"})}
	if err := newFolders(store).Create(context.Background(), "me@example.test", "FATURAS"); !errors.Is(err, ErrFolderExists) {
		t.Errorf("returned %v", err)
	}
}

// The standard folders have meaning to the protocol: INBOX is where delivery
// goes and Trash is where deletion goes. Letting a user create, rename or
// delete one would break the mailbox in ways no error message explains.
func TestRefusesToTouchTheStandardFolders(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	uc := newFolders(store)

	for _, name := range []string{"INBOX", "inbox", "Sent", "Drafts", "Trash", "Junk", "Archive"} {
		if err := uc.Delete(context.Background(), "me@example.test", name); !errors.Is(err, ErrFolderReserved) {
			t.Errorf("deleting %q returned %v", name, err)
		}
		if err := uc.Rename(context.Background(), "me@example.test", name, "Outra"); !errors.Is(err, ErrFolderReserved) {
			t.Errorf("renaming %q returned %v", name, err)
		}
	}
	if len(store.deleted) != 0 || len(store.renamed) != 0 {
		t.Errorf("a reserved folder was still touched: deleted=%v renamed=%v", store.deleted, store.renamed)
	}
}

// Reports is created by this server for its own use, and delivery files into
// it by name.
func TestRefusesToTouchTheReportsFolder(t *testing.T) {
	store := &stubFolderStore{existing: append(standardFolders(), port.WebmailFolder{Name: "Reports"})}
	if err := newFolders(store).Delete(context.Background(), "me@example.test", "Reports"); !errors.Is(err, ErrFolderReserved) {
		t.Errorf("returned %v", err)
	}
}

func TestRenamesACustomFolder(t *testing.T) {
	store := &stubFolderStore{existing: append(standardFolders(), port.WebmailFolder{Name: "Faturas"})}
	if err := newFolders(store).Rename(context.Background(), "me@example.test", "Faturas", "Financeiro"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if len(store.renamed) != 1 || store.renamed[0] != "Faturas->Financeiro" {
		t.Errorf("renamed %v", store.renamed)
	}
}

func TestRenameRefusesAnExistingName(t *testing.T) {
	store := &stubFolderStore{existing: append(standardFolders(),
		port.WebmailFolder{Name: "Faturas"}, port.WebmailFolder{Name: "Financeiro"})}
	if err := newFolders(store).Rename(context.Background(), "me@example.test", "Faturas", "Financeiro"); !errors.Is(err, ErrFolderExists) {
		t.Errorf("returned %v", err)
	}
}

func TestDeletesACustomFolder(t *testing.T) {
	store := &stubFolderStore{existing: append(standardFolders(), port.WebmailFolder{Name: "Faturas"})}
	if err := newFolders(store).Delete(context.Background(), "me@example.test", "Faturas"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "Faturas" {
		t.Errorf("deleted %v", store.deleted)
	}
}

func TestDeletingAFolderThatIsNotThereIsReported(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	if err := newFolders(store).Delete(context.Background(), "me@example.test", "Faturas"); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("returned %v", err)
	}
}

// A name with a delimiter in it would look like a nested folder to IMAP, and
// this server does not create those.
func TestRefusesADelimiterInTheName(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	for _, name := range []string{"a/b", "a.b", `a\b`} {
		if err := newFolders(store).Create(context.Background(), "me@example.test", name); !errors.Is(err, ErrInvalidFolderName) {
			t.Errorf("creating %q returned %v", name, err)
		}
	}
}

func TestRefusesAnAbsurdlyLongName(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	err := newFolders(store).Create(context.Background(), "me@example.test", strings.Repeat("a", 300))
	if !errors.Is(err, ErrInvalidFolderName) {
		t.Errorf("returned %v", err)
	}
}

// Accents are ordinary in a Portuguese folder name.
func TestAcceptsAccentedNames(t *testing.T) {
	store := &stubFolderStore{existing: standardFolders()}
	if err := newFolders(store).Create(context.Background(), "me@example.test", "Relatórios de Março"); err != nil {
		t.Errorf("an accented name was refused: %v", err)
	}
}
