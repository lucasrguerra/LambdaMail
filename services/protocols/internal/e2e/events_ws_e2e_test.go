package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"

	appusecase "lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/diskstorage"
	"lambdamail/protocols/internal/infrastructure/postgres"
	httppresentation "lambdamail/protocols/internal/presentation/http"
)

// A message delivered over SMTP has to reach an open browser without the page
// polling for it. This drives the whole chain: delivery writes the outbox row
// in its transaction, the relay picks it up, the hub pushes it.
func TestEventStreamPushesDeliveredMail(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test skipped in -short mode")
	}
	ctx := context.Background()

	runtimeDir := t.TempDir()
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(54348).
		RuntimePath(runtimeDir).
		StartTimeout(120 * time.Second))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	defer pg.Stop()

	pool, err := postgres.NewPool(ctx, "postgres://postgres:postgres@localhost:54348/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	defer pool.Close()
	applyMigrations(t, ctx, pool)

	domainID, mailboxID := uuid.New(), uuid.New()
	const address = "live@events.example"
	if _, err := pool.Exec(ctx,
		`INSERT INTO domains (id, name, punycode_name) VALUES ($1, 'events.example', 'events.example')`, domainID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO mailboxes (id, domain_id, local_part, email_address, password_hash)
		 VALUES ($1, $2, 'live', $3, 'x')`, mailboxID, domainID, address); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO folders (mailbox_id, name, special_use) VALUES ($1, 'INBOX', 'inbox')`, mailboxID); err != nil {
		t.Fatal(err)
	}

	webmailUC := appusecase.NewWebmailUseCase(
		postgres.NewWebmailRepository(pool), diskstorage.NewLocalDiskBlobReader(pool), nil, nil, "mail.events.example")
	hub := httppresentation.NewEventHub(httppresentation.NewWebSessionVerifier(webmailSecret), webmailUC)

	router := httppresentation.NewRouter(nil, func() error { return nil })
	router.SetMailAPI(webmailUC, webmailSecret)
	router.SetEventHub(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	// Connect before delivering, the way an open tab would be.
	header := http.Header{}
	header.Set("Cookie", "lm_user_session="+mintSession(t, address, "user", time.Hour))
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/events",
		&websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.CloseNow()

	// Wait for the hub to register the subscriber before publishing.
	deadline := time.Now().Add(5 * time.Second)
	for hub.SubscriberCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.SubscriberCount() != 1 {
		t.Fatalf("hub has %d subscribers, want 1", hub.SubscriberCount())
	}

	inbound := appusecase.NewProcessInboundEmailUseCase(
		postgres.NewMailboxRepository(pool),
		diskstorage.NewLocalDiskBlobStorage(pool, t.TempDir()),
		postgres.NewInboundMessageRepository(pool))
	targets, err := inbound.ResolveRecipient(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	if err := inbound.Handle(ctx, appusecase.ProcessInboundEmailInput{
		Sender:             "sender@remote.test",
		Recipients:         targets,
		RecipientAddresses: []string{address},
		Body:               strings.NewReader("From: s@remote.test\r\nSubject: live\r\n\r\nbody\r\n"),
	}); err != nil {
		t.Fatal(err)
	}

	// The relay is driven explicitly so the test does not race its ticker.
	relay := appusecase.NewOutboxRelay(postgres.NewOutboxRepository(pool), hub)
	published, err := relay.PublishBatch(ctx)
	if err != nil {
		t.Fatalf("publish batch: %v", err)
	}
	if published != 1 {
		t.Fatalf("published %d events, want 1", published)
	}

	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var received httppresentation.EventMessage
	if err := wsjson.Read(readCtx, conn, &received); err != nil {
		t.Fatalf("no event arrived: %v", err)
	}

	if received.Type != "EmailReceived" {
		t.Errorf("event type = %q, want EmailReceived", received.Type)
	}
	if received.MailboxID != mailboxID.String() {
		t.Errorf("mailbox = %q, want %q", received.MailboxID, mailboxID)
	}
	var payload map[string]any
	if err := json.Unmarshal(received.Payload, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if payload["recipient"] != address {
		t.Errorf("payload recipient = %v, want %s", payload["recipient"], address)
	}

	// A published event must not be delivered twice on the next pass.
	again, err := relay.PublishBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Errorf("the relay republished %d already-published events", again)
	}
}

// The stream is per mailbox: another account's arrival must not be visible.
func TestEventStreamRequiresUserSession(t *testing.T) {
	hub := httppresentation.NewEventHub(
		httppresentation.NewWebSessionVerifier(webmailSecret),
		appusecase.NewWebmailUseCase(nil, nil, nil, nil, "mail.test"))
	router := httppresentation.NewRouter(nil, func() error { return nil })
	router.SetEventHub(hub)
	server := httptest.NewServer(router)
	defer server.Close()

	for _, tc := range []struct{ name, cookie string }{
		{"no cookie", ""},
		{"admin session", "lm_user_session=" + mintSession(t, "admin@x.test", "admin", time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
			if tc.cookie != "" {
				req.Header.Set("Cookie", tc.cookie)
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
