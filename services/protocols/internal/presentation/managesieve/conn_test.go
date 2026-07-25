package managesievepresentation

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	appusecase "lambdamail/protocols/internal/application/usecase"
)

type fakeAuthRepository struct {
	byAddress map[string]port.MailboxAuth
}

func (f *fakeAuthRepository) FindByAddress(_ context.Context, address string) (*port.MailboxAuth, error) {
	rec, ok := f.byAddress[address]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

type fakeSieveRepository struct {
	scripts map[string]map[string]port.SieveScriptRecord
}

func newFakeSieveRepository() *fakeSieveRepository {
	return &fakeSieveRepository{scripts: make(map[string]map[string]port.SieveScriptRecord)}
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

func setupManageSieveServer(t *testing.T) (net.Listener, func()) {
	t.Helper()
	password := "correct horse battery staple"
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	mailboxID := uuid.New()

	auth := &fakeAuthRepository{byAddress: map[string]port.MailboxAuth{
		"user@example.test": {ID: mailboxID, PasswordHash: hash},
	}}
	sieveRepo := newFakeSieveRepository()

	uc := appusecase.NewManageSieveSessionUseCase(auth, sieveRepo)
	srv := NewServer("127.0.0.1:0", uc, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)

	return ln, func() {
		ln.Close()
	}
}

func TestManageSieveServer_FullLifecycle(t *testing.T) {
	ln, cleanup := setupManageSieveServer(t)
	defer cleanup()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	reader := bufio.NewReader(c)
	writer := bufio.NewWriter(c)

	readLine := func() string {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}

	sendCmd := func(cmd string) {
		writer.WriteString(cmd + "\r\n")
		writer.Flush()
	}

	// 1. Read greeting capabilities
	for {
		l := readLine()
		if strings.HasPrefix(l, "OK") {
			break
		}
	}

	// 2. CAPABILITY
	sendCmd("CAPABILITY")
	for {
		l := readLine()
		if strings.HasPrefix(l, "OK") {
			break
		}
	}

	// 3. AUTHENTICATE PLAIN
	saslPayload := base64.StdEncoding.EncodeToString([]byte("\x00user@example.test\x00correct horse battery staple"))
	sendCmd(fmt.Sprintf("AUTHENTICATE \"PLAIN\" %s", saslPayload))
	if authResp := readLine(); !strings.HasPrefix(authResp, "OK") {
		t.Fatalf("AUTH resp = %q, want OK", authResp)
	}

	// 4. CHECKSCRIPT
	scriptContent := `require ["fileinto"]; if header :is "Subject" "Spam" { fileinto "Junk"; }`
	sendCmd(fmt.Sprintf("CHECKSCRIPT {%d+}\r\n%s", len(scriptContent), scriptContent))
	if checkResp := readLine(); !strings.HasPrefix(checkResp, "OK") {
		t.Fatalf("CHECKSCRIPT resp = %q", checkResp)
	}

	// 5. PUTSCRIPT
	sendCmd(fmt.Sprintf("PUTSCRIPT \"my_rules\" {%d+}\r\n%s", len(scriptContent), scriptContent))
	if putResp := readLine(); !strings.HasPrefix(putResp, "OK") {
		t.Fatalf("PUTSCRIPT resp = %q", putResp)
	}

	// 6. GETSCRIPT
	sendCmd("GETSCRIPT \"my_rules\"")
	getLiteralHead := readLine()
	if !strings.HasPrefix(getLiteralHead, "{") {
		t.Fatalf("GETSCRIPT literal head = %q", getLiteralHead)
	}
	getScriptContent := readLine()
	if getScriptContent != scriptContent {
		t.Errorf("GETSCRIPT content = %q, want %q", getScriptContent, scriptContent)
	}
	if getOk := readLine(); !strings.HasPrefix(getOk, "OK") {
		t.Fatalf("GETSCRIPT term = %q", getOk)
	}

	// 7. LISTSCRIPTS
	sendCmd("LISTSCRIPTS")
	listLine := readLine()
	if !strings.Contains(listLine, `"my_rules"`) {
		t.Fatalf("LISTSCRIPTS line = %q", listLine)
	}
	if listOk := readLine(); !strings.HasPrefix(listOk, "OK") {
		t.Fatalf("LISTSCRIPTS term = %q", listOk)
	}

	// 8. SETACTIVE
	sendCmd("SETACTIVE \"my_rules\"")
	if setActiveOk := readLine(); !strings.HasPrefix(setActiveOk, "OK") {
		t.Fatalf("SETACTIVE term = %q", setActiveOk)
	}

	// 9. LOGOUT
	sendCmd("LOGOUT")
	if logoutOk := readLine(); !strings.HasPrefix(logoutOk, "OK") {
		t.Fatalf("LOGOUT term = %q", logoutOk)
	}
}
