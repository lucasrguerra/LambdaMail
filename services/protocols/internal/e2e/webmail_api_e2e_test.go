package e2e

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	httppresentation "lambdamail/protocols/internal/presentation/http"
)

const webmailSecret = "webmail-e2e-shared-secret"

// mintSession builds the token the auth service would issue, using the same
// HS256 construction, so this test exercises the real verification path.
func mintSession(t *testing.T, email, surface string, ttl time.Duration) string {
	t.Helper()

	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}

	header := encode(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload := encode(map[string]any{
		"sub": uuid.NewString(), "email": email, "role": "USER", "domainId": uuid.NewString(),
		"surface": surface, "aud": "lambdamail:" + surface, "mfaSatisfied": true,
		"purpose": "session", "iat": time.Now().Unix(), "exp": time.Now().Add(ttl).Unix(),
	})

	mac := hmac.New(sha256.New, []byte(webmailSecret))
	mac.Write([]byte(header + "." + payload))
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestWebmailApiEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54346).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	pool, err := postgres.NewPool(ctx, "postgres://postgres:postgres@localhost:54346/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()
	applyMigrations(t, ctx, pool)

	// ------------------------------------------------------------ fixtures
	domainID, mailboxID, folderID := uuid.New(), uuid.New(), uuid.New()
	const address = "reader@webmail.example"
	if _, err := pool.Exec(ctx,
		`INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'webmail.example', 'webmail.example')`, domainID); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash)
		 VALUES ($1, $2, 'reader', $3, 'x')`, mailboxID, domainID, address); err != nil {
		t.Fatalf("seed mailbox: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO folders (id, mailbox_id, name, special_use) VALUES ($1, $2, 'INBOX', 'inbox')`,
		folderID, mailboxID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	// ------------------------------------------------- deliver a real message
	blobs := diskstorage.NewLocalDiskBlobStorage(pool, t.TempDir())
	inbound := appusecase.NewProcessInboundEmailUseCase(
		postgres.NewMailboxRepository(pool), blobs, postgres.NewInboundMessageRepository(pool))

	raw := "From: Alice Sender <alice@remote.test>\r\n" +
		"To: " + address + "\r\n" +
		"Subject: =?UTF-8?Q?Relat=C3=B3rio?=\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
		"Corpo da mensagem de teste.\r\n"

	targets, err := inbound.ResolveRecipient(ctx, address)
	if err != nil {
		t.Fatalf("resolve recipient: %v", err)
	}
	if err := inbound.Handle(ctx, appusecase.ProcessInboundEmailInput{
		Sender:             "alice@remote.test",
		Recipients:         targets,
		RecipientAddresses: []string{address},
		Body:               strings.NewReader(raw),
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	// ------------------------------------------------------------ the API
	webmailUC := appusecase.NewWebmailUseCase(
		postgres.NewWebmailRepository(pool),
		diskstorage.NewLocalDiskBlobReader(pool),
		nil, nil, "mail.webmail.example",
	)
	router := httppresentation.NewRouter(nil, func() error { return nil })
	router.SetMailAPI(webmailUC, webmailSecret)
	server := httptest.NewServer(router)
	defer server.Close()

	token := mintSession(t, address, "user", time.Hour)
	call := func(method, path string, body any) (*http.Response, []byte) {
		var reader *bytes.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		} else {
			reader = bytes.NewReader(nil)
		}
		req, err := http.NewRequest(method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return resp, buf.Bytes()
	}

	// 1. Folders come back with the seeded INBOX and its unread counter.
	resp, body := call(http.MethodGet, "/api/v1/mail/folders", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("folders: status %d body %s", resp.StatusCode, body)
	}
	var folders []struct {
		Name        string `json:"name"`
		SpecialUse  string `json:"special_use"`
		UnreadCount int    `json:"unread_count"`
	}
	if err := json.Unmarshal(body, &folders); err != nil {
		t.Fatalf("decode folders: %v (%s)", err, body)
	}
	if len(folders) != 1 || folders[0].SpecialUse != "inbox" || folders[0].UnreadCount != 1 {
		t.Fatalf("folders = %+v, want one inbox with 1 unread", folders)
	}

	// 2. The listing carries the header metadata, decoded.
	resp, body = call(http.MethodGet, "/api/v1/mail/messages?folder=inbox", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("messages: status %d body %s", resp.StatusCode, body)
	}
	var messages []struct {
		UID      uint32 `json:"uid"`
		Subject  string `json:"subject"`
		FromName string `json:"from_display_name"`
		Snippet  string `json:"snippet"`
		Seen     bool   `json:"seen"`
	}
	if err := json.Unmarshal(body, &messages); err != nil {
		t.Fatalf("decode messages: %v (%s)", err, body)
	}
	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}
	if messages[0].Subject != "Relatório" {
		t.Errorf("Subject = %q, want the decoded encoded-word", messages[0].Subject)
	}
	if messages[0].FromName != "Alice Sender" {
		t.Errorf("FromName = %q", messages[0].FromName)
	}
	if messages[0].Snippet == "" {
		t.Error("Snippet is empty; the list would show no preview")
	}
	if messages[0].Seen {
		t.Error("a freshly delivered message is already marked seen")
	}

	// 3. Fetching the body returns the original bytes and marks it read.
	resp, body = call(http.MethodGet, fmt.Sprintf("/api/v1/mail/message/%d?folder=inbox", messages[0].UID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("message: status %d body %s", resp.StatusCode, body)
	}
	var message struct {
		Subject string `json:"subject"`
		From    string `json:"from"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(body, &message); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if !strings.Contains(message.Text, "Corpo da mensagem de teste.") {
		t.Errorf("the rendered body is not the stored message: %q", message.Text)
	}
	if message.Subject != "Relatório" {
		t.Errorf("rendered Subject = %q, want the decoded encoded-word", message.Subject)
	}
	if !strings.Contains(message.From, "alice@remote.test") {
		t.Errorf("rendered From = %q", message.From)
	}

	_, body = call(http.MethodGet, "/api/v1/mail/messages?folder=inbox", nil)
	_ = json.Unmarshal(body, &messages)
	if !messages[0].Seen {
		t.Error("opening the message did not mark it seen")
	}

	var unread int
	if err := pool.QueryRow(ctx, `SELECT unread_count FROM folders WHERE id = $1`, folderID).Scan(&unread); err != nil {
		t.Fatal(err)
	}
	if unread != 0 {
		t.Errorf("unread_count = %d after reading the only message, want 0", unread)
	}

	// 4. Search matches on the indexed columns.
	_, body = call(http.MethodGet, "/api/v1/mail/messages?folder=inbox&q=Relat", nil)
	_ = json.Unmarshal(body, &messages)
	if len(messages) != 1 {
		t.Errorf("search returned %d messages, want 1", len(messages))
	}
	_, body = call(http.MethodGet, "/api/v1/mail/messages?folder=inbox&q=nothing-matches-this", nil)
	_ = json.Unmarshal(body, &messages)
	if len(messages) != 0 {
		t.Errorf("search returned %d messages for a non-matching term", len(messages))
	}
}

// One account must not be able to read another's mail by guessing a UID.
func TestWebmailApiRejectsCrossMailboxAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54347).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	pool, err := postgres.NewPool(ctx, "postgres://postgres:postgres@localhost:54347/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()
	applyMigrations(t, ctx, pool)

	domainID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'iso.example', 'iso.example')`, domainID); err != nil {
		t.Fatal(err)
	}

	victim, attacker := uuid.New(), uuid.New()
	for id, local := range map[uuid.UUID]string{victim: "victim", attacker: "attacker"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash)
			 VALUES ($1, $2, $3, $4, 'x')`, id, domainID, local, local+"@iso.example"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1, 'INBOX', 'inbox')`, id); err != nil {
			t.Fatal(err)
		}
	}

	blobs := diskstorage.NewLocalDiskBlobStorage(pool, t.TempDir())
	inbound := appusecase.NewProcessInboundEmailUseCase(
		postgres.NewMailboxRepository(pool), blobs, postgres.NewInboundMessageRepository(pool))
	targets, err := inbound.ResolveRecipient(ctx, "victim@iso.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := inbound.Handle(ctx, appusecase.ProcessInboundEmailInput{
		Sender:             "someone@remote.test",
		Recipients:         targets,
		RecipientAddresses: []string{"victim@iso.example"},
		Body:               strings.NewReader("From: s@remote.test\r\nSubject: private\r\n\r\nsecret\r\n"),
	}); err != nil {
		t.Fatal(err)
	}

	webmailUC := appusecase.NewWebmailUseCase(
		postgres.NewWebmailRepository(pool), diskstorage.NewLocalDiskBlobReader(pool), nil, nil, "mail.iso.example")
	router := httppresentation.NewRouter(nil, func() error { return nil })
	router.SetMailAPI(webmailUC, webmailSecret)
	server := httptest.NewServer(router)
	defer server.Close()

	// The victim's only message has UID 1; the attacker asks for exactly that.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/mail/message/1?folder=inbox", nil)
	req.Header.Set("Authorization", "Bearer "+mintSession(t, "attacker@iso.example", "user", time.Hour))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: one mailbox read another's message", resp.StatusCode)
	}
}

// An admin token must not open the user mail API, and neither must no token.
func TestWebmailApiRequiresUserSession(t *testing.T) {
	router := httppresentation.NewRouter(nil, func() error { return nil })
	router.SetMailAPI(appusecase.NewWebmailUseCase(nil, nil, nil, nil, "mail.test"), webmailSecret)
	server := httptest.NewServer(router)
	defer server.Close()

	for _, tc := range []struct{ name, token string }{
		{"no token", ""},
		{"admin token", mintSession(t, "admin@iso.example", "admin", time.Hour)},
		{"forged token", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.AAAA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/mail/folders", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", resp.StatusCode)
			}
		})
	}
}
