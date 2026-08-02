package httppresentation

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

// subscriberBuffer is how many events may queue for one connection before it
// is considered stuck. A browser tab that stops reading must not be able to
// hold the relay's memory: it is dropped and reconciles on reconnect.
const subscriberBuffer = 32

// EventMessage is what a subscriber receives.
type EventMessage struct {
	Type      string          `json:"type"`
	MailboxID string          `json:"mailbox_id"`
	Payload   json.RawMessage `json:"payload"`
}

type subscriber struct {
	mailboxID string
	events    chan EventMessage
}

// EventHub fans domain events out to the browser over WebSocket.
//
// Delivery is per mailbox: an event is only sent to connections whose session
// resolved to that mailbox, so one account's arrivals are never visible to
// another even though they share this process.
type EventHub struct {
	mu          sync.RWMutex
	subscribers map[*subscriber]struct{}
	sessions    *WebSessionVerifier
	mailboxes   *usecase.WebmailUseCase
}

func NewEventHub(sessions *WebSessionVerifier, mailboxes *usecase.WebmailUseCase) *EventHub {
	return &EventHub{
		subscribers: map[*subscriber]struct{}{},
		sessions:    sessions,
		mailboxes:   mailboxes,
	}
}

// Publish implements port.EventPublisher. It never blocks: a subscriber whose
// buffer is full is dropped rather than allowed to stall the relay.
func (h *EventHub) Publish(event port.OutboxEvent) {
	message := EventMessage{
		Type:      event.EventType,
		MailboxID: event.AggregateID.String(),
		Payload:   json.RawMessage(event.Payload),
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subscribers {
		if sub.mailboxID != message.MailboxID {
			continue
		}
		select {
		case sub.events <- message:
		default:
			// Dropped on purpose; the client resynchronises when it reconnects.
			logDroppedSubscriber(sub.mailboxID)
		}
	}
}

func (h *EventHub) add(sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribers[sub] = struct{}{}
}

func (h *EventHub) remove(sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subscribers, sub)
	close(sub.events)
}

// SubscriberCount is used by tests and by the health surface.
func (h *EventHub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}

// ServeHTTP upgrades an authenticated request to a WebSocket.
func (h *EventHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The session is checked before the upgrade: an unauthenticated caller
	// gets a plain 401 and never reaches the WebSocket machinery.
	token := ""
	if cookie, err := r.Cookie("lm_user_session"); err == nil {
		token = cookie.Value
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "User session required")
		return
	}

	session, err := h.sessions.RequireSurface(token, "user")
	if err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "User session required")
		return
	}

	mailboxID, err := h.mailboxes.MailboxIDFor(r.Context(), session.Email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "User session required")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The browser reaches this through the webmail's own origin via the
		// Next proxy, so cross-origin upgrades have no legitimate caller.
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	sub := &subscriber{mailboxID: mailboxID, events: make(chan EventMessage, subscriberBuffer)}
	h.add(sub)
	defer h.remove(sub)

	ctx := conn.CloseRead(r.Context())

	// Pings keep an idle connection alive through proxies that would otherwise
	// close it, and detect a peer that vanished without a FIN.
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.events:
			if !ok {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(writeCtx, conn, event)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// SetEventHub registers the real-time endpoint.
func (r *Router) SetEventHub(hub *EventHub) {
	r.events = hub
}

func (r *Router) handleEvents(w http.ResponseWriter, req *http.Request) {
	if r.events == nil {
		writeError(w, http.StatusServiceUnavailable, "EVENTS_DISABLED",
			"The event stream needs JWT_SECRET to verify webmail sessions")
		return
	}
	r.events.ServeHTTP(w, req)
}

var _ port.EventPublisher = (*EventHub)(nil)

// logDroppedSubscriber records that a connection could not keep up. It is a
// signal worth having: a client that drops events repeatedly is one whose
// inbox will look stale until it reconnects.
func logDroppedSubscriber(mailboxID string) {
	log.Printf("events: dropping an event for a slow subscriber on mailbox %s", mailboxID)
}
