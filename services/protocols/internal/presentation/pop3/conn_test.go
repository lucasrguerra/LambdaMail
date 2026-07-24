package pop3presentation

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

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

type fakeImapFolderRepository struct {
	byName map[string]port.ImapFolderRecord
}

func (f *fakeImapFolderRepository) ListFolders(_ context.Context, _ string) ([]port.ImapFolderRecord, error) {
	var out []port.ImapFolderRecord
	for _, rec := range f.byName {
		out = append(out, rec)
	}
	return out, nil
}

func (f *fakeImapFolderRepository) FindByName(_ context.Context, _ string, name string) (*port.ImapFolderRecord, error) {
	rec, ok := f.byName[name]
	if !ok {
		return nil, nil
	}
	return &rec, nil
}

type fakeMessageQueryRepository struct {
	messages []port.MessageRecord
}

func (f *fakeMessageQueryRepository) ListMessages(_ context.Context, _ string) ([]port.MessageRecord, error) {
	return f.messages, nil
}

type fakeBlobReader struct {
	content map[uuid.UUID][]byte
}

func (f *fakeBlobReader) ReadByID(_ context.Context, blobID uuid.UUID) ([]byte, error) {
	return f.content[blobID], nil
}

type fakeExpungeRepository struct {
	calls []struct {
		folderID string
		uids     []uint32
	}
}

func (f *fakeExpungeRepository) Expunge(_ context.Context, folderID string, uids []uint32) error {
	f.calls = append(f.calls, struct {
		folderID string
		uids     []uint32
	}{folderID, uids})
	return nil
}

func setupPop3Server(t *testing.T, messages []port.MessageRecord, blobs map[uuid.UUID][]byte, expunger *fakeExpungeRepository) (net.Listener, func()) {
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
	folders := &fakeImapFolderRepository{byName: map[string]port.ImapFolderRecord{
		"INBOX": {ID: "folder-1", Name: "INBOX", NumMessages: uint32(len(messages))},
	}}
	query := &fakeMessageQueryRepository{messages: messages}
	reader := &fakeBlobReader{content: blobs}

	uc := appusecase.NewPop3SessionUseCase(auth, folders, query, reader, expunger)
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

func TestPop3Server_Capa_User_Pass_Stat_List_Retr_Dele_Quit(t *testing.T) {
	blobID := uuid.New()
	msgContent := []byte("From: alice@example.test\r\nSubject: Test\r\n\r\n.Dot-leading line\r\nHello World\r\n")
	messages := []port.MessageRecord{
		{UID: 101, BlobID: blobID, SizeBytes: int64(len(msgContent))},
	}
	blobs := map[uuid.UUID][]byte{blobID: msgContent}
	expunger := &fakeExpungeRepository{}

	ln, cleanup := setupPop3Server(t, messages, blobs, expunger)
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

	// 1. Greeting
	greeting := readLine()
	if !strings.HasPrefix(greeting, "+OK") {
		t.Fatalf("greeting = %q, want +OK", greeting)
	}

	// 2. CAPA
	sendCmd("CAPA")
	capaLine := readLine()
	if !strings.HasPrefix(capaLine, "+OK") {
		t.Fatalf("CAPA resp = %q", capaLine)
	}
	for {
		l := readLine()
		if l == "." {
			break
		}
	}

	// 3. USER & PASS
	sendCmd("USER user@example.test")
	if l := readLine(); !strings.HasPrefix(l, "+OK") {
		t.Fatalf("USER resp = %q", l)
	}

	sendCmd("PASS correct horse battery staple")
	if l := readLine(); !strings.HasPrefix(l, "+OK") {
		t.Fatalf("PASS resp = %q", l)
	}

	// 4. STAT
	sendCmd("STAT")
	statResp := readLine()
	if !strings.HasPrefix(statResp, "+OK 1 ") {
		t.Fatalf("STAT resp = %q, want 1 message", statResp)
	}

	// 5. LIST
	sendCmd("LIST")
	listHead := readLine()
	if !strings.HasPrefix(listHead, "+OK") {
		t.Fatalf("LIST resp = %q", listHead)
	}
	msg1List := readLine()
	if !strings.HasPrefix(msg1List, "1 ") {
		t.Fatalf("LIST item = %q", msg1List)
	}
	if dot := readLine(); dot != "." {
		t.Fatalf("LIST term = %q", dot)
	}

	// 6. UIDL
	sendCmd("UIDL")
	uidlHead := readLine()
	if !strings.HasPrefix(uidlHead, "+OK") {
		t.Fatalf("UIDL resp = %q", uidlHead)
	}
	uidlItem := readLine()
	if uidlItem != "1 101" {
		t.Fatalf("UIDL item = %q, want '1 101'", uidlItem)
	}
	if dot := readLine(); dot != "." {
		t.Fatalf("UIDL term = %q", dot)
	}

	// 7. RETR 1 (verifying dot-stuffing ..Dot-leading line)
	sendCmd("RETR 1")
	retrHead := readLine()
	if !strings.HasPrefix(retrHead, "+OK") {
		t.Fatalf("RETR resp = %q", retrHead)
	}
	var retrLines []string
	for {
		l := readLine()
		if l == "." {
			break
		}
		retrLines = append(retrLines, l)
	}
	if len(retrLines) < 4 {
		t.Fatalf("retrLines = %v", retrLines)
	}
	if retrLines[3] != "..Dot-leading line" {
		t.Errorf("dot-stuffed line = %q, want '..Dot-leading line'", retrLines[3])
	}

	// 8. DELE 1
	sendCmd("DELE 1")
	deleResp := readLine()
	if !strings.HasPrefix(deleResp, "+OK") {
		t.Fatalf("DELE resp = %q", deleResp)
	}

	// 9. QUIT (commits expunge)
	sendCmd("QUIT")
	quitResp := readLine()
	if !strings.HasPrefix(quitResp, "+OK") {
		t.Fatalf("QUIT resp = %q", quitResp)
	}

	time.Sleep(50 * time.Millisecond)

	if len(expunger.calls) != 1 || len(expunger.calls[0].uids) != 1 || expunger.calls[0].uids[0] != 101 {
		t.Errorf("expunged calls = %+v, want UID 101", expunger.calls)
	}
}

func TestPop3Server_Top_Rset_AuthFailures(t *testing.T) {
	blobID := uuid.New()
	msgContent := []byte("From: bob@example.test\r\nSubject: Top test\r\n\r\nLine 1\r\nLine 2\r\nLine 3\r\n")
	messages := []port.MessageRecord{
		{UID: 202, BlobID: blobID, SizeBytes: int64(len(msgContent))},
	}
	blobs := map[uuid.UUID][]byte{blobID: msgContent}
	expunger := &fakeExpungeRepository{}

	ln, cleanup := setupPop3Server(t, messages, blobs, expunger)
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

	readLine() // Greeting

	// Test wrong password
	sendCmd("USER user@example.test")
	readLine()
	sendCmd("PASS wrong-password")
	if l := readLine(); !strings.HasPrefix(l, "-ERR") {
		t.Fatalf("wrong pass resp = %q, want -ERR", l)
	}

	// Login correctly
	sendCmd("USER user@example.test")
	readLine()
	sendCmd("PASS correct horse battery staple")
	readLine()

	// TOP 1 2 (fetch headers + 2 body lines)
	sendCmd("TOP 1 2")
	topHead := readLine()
	if !strings.HasPrefix(topHead, "+OK") {
		t.Fatalf("TOP resp = %q", topHead)
	}
	var topLines []string
	for {
		l := readLine()
		if l == "." {
			break
		}
		topLines = append(topLines, l)
	}
	if len(topLines) < 4 {
		t.Fatalf("topLines = %v", topLines)
	}

	// DELE then RSET
	sendCmd("DELE 1")
	readLine()
	sendCmd("RSET")
	if rsetResp := readLine(); !strings.HasPrefix(rsetResp, "+OK") {
		t.Fatalf("RSET resp = %q", rsetResp)
	}

	// QUIT without deleting anything
	sendCmd("QUIT")
	readLine()

	time.Sleep(50 * time.Millisecond)
	if len(expunger.calls) != 0 {
		t.Errorf("expected 0 expunge calls after RSET, got %d", len(expunger.calls))
	}
}
