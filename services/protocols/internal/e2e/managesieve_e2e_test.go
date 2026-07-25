package e2e

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/argon2id"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/postgres"
	managesievepresentation "lambdamail/protocols/internal/presentation/managesieve"
)

func TestManageSieveEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54333). // distinct port for ManageSieve e2e test execution
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	dbURL := "postgres://postgres:postgres@localhost:54333/postgres?sslmode=disable"
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	root := repoRoot(t)
	sql, err := os.ReadFile(filepath.Join(root, "migrations", "0001_init_schema.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	// --- Seed domain & mailbox -----------------------------------------------
	password := "correct horse battery staple"
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	domainID := "00000000-0000-0000-0000-000000000001"
	mailboxID := "00000000-0000-0000-0000-000000000002"
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'example.test', 'example.test')`, domainID); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash) VALUES ($1, $2, 'user', 'user@example.test', $3)`, mailboxID, domainID, hash); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}

	// Setup ManageSieve Server
	sieveUC := appusecase.NewManageSieveSessionUseCase(
		postgres.NewAuthRepository(pool),
		postgres.NewSieveRepository(pool),
	)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	srv := managesievepresentation.NewServer(ln.Addr().String(), sieveUC, nil)
	go srv.Serve(ln)
	defer srv.Close()

	// Dial ManageSieve server
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial ManageSieve: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	readLine := func() string {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read line: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}

	sendCmd := func(cmd string) {
		writer.WriteString(cmd + "\r\n")
		writer.Flush()
	}

	// 1. Greeting
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
	gotScript := readLine()
	if gotScript != scriptContent {
		t.Fatalf("GETSCRIPT content = %q, want %q", gotScript, scriptContent)
	}
	if getOk := readLine(); !strings.HasPrefix(getOk, "OK") {
		t.Fatalf("GETSCRIPT ok = %q", getOk)
	}

	// 7. LISTSCRIPTS
	sendCmd("LISTSCRIPTS")
	listLine := readLine()
	if !strings.Contains(listLine, `"my_rules"`) {
		t.Fatalf("LISTSCRIPTS line = %q", listLine)
	}
	if listOk := readLine(); !strings.HasPrefix(listOk, "OK") {
		t.Fatalf("LISTSCRIPTS ok = %q", listOk)
	}

	// 8. SETACTIVE
	sendCmd("SETACTIVE \"my_rules\"")
	if setActiveOk := readLine(); !strings.HasPrefix(setActiveOk, "OK") {
		t.Fatalf("SETACTIVE ok = %q", setActiveOk)
	}

	// Verify in DB that script is active
	var isActive bool
	if err := pool.QueryRow(ctx, `SELECT is_active FROM sieve_scripts WHERE mailbox_id = $1 AND name = 'my_rules'`, mailboxID).Scan(&isActive); err != nil || !isActive {
		t.Fatalf("expected is_active=true in DB, err=%v, isActive=%v", err, isActive)
	}

	// 9. RENAMESCRIPT
	sendCmd("RENAMESCRIPT \"my_rules\" \"active_rules\"")
	if renameOk := readLine(); !strings.HasPrefix(renameOk, "OK") {
		t.Fatalf("RENAMESCRIPT ok = %q", renameOk)
	}

	// 10. HAVESPACE
	sendCmd("HAVESPACE \"active_rules\" 500")
	if spaceOk := readLine(); !strings.HasPrefix(spaceOk, "OK") {
		t.Fatalf("HAVESPACE ok = %q", spaceOk)
	}

	// 11. Deactivate & DELETE
	sendCmd("SETACTIVE \"\"")
	if deactOk := readLine(); !strings.HasPrefix(deactOk, "OK") {
		t.Fatalf("deactivate ok = %q", deactOk)
	}

	sendCmd("DELETESCRIPT \"active_rules\"")
	if delOk := readLine(); !strings.HasPrefix(delOk, "OK") {
		t.Fatalf("DELETESCRIPT ok = %q", delOk)
	}

	// Verify in DB that script was deleted
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sieve_scripts WHERE mailbox_id = $1`, mailboxID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expected 0 scripts in DB, got count=%d, err=%v", count, err)
	}

	// 12. LOGOUT
	sendCmd("LOGOUT")
	if logoutOk := readLine(); !strings.HasPrefix(logoutOk, "OK") {
		t.Fatalf("LOGOUT ok = %q", logoutOk)
	}
}
