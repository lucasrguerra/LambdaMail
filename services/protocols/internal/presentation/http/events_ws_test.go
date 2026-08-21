package httppresentation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

// The event stream tells a browser its mailbox changed. It is the one place
// where one account's activity is pushed out over another's connection if the
// filtering is wrong, and none of it was covered.

func newTestHub(t *testing.T, store *fakeMailStore) *EventHub {
	t.Helper()
	uc := usecase.NewWebmailUseCase(store, &fakeBlobReader{}, nil, nil, "mail.example.test")
	return NewEventHub(testVerifier(interopSecret), uc)
}

// The hub matches on the aggregate's UUID string, so subscribers are keyed by
// real UUIDs rather than the readable names the mail API fakes use.
var (
	mailboxOne = uuid.New()
	mailboxTwo = uuid.New()
)

// subscribeTo registers a subscriber directly, which is what the WebSocket
// handler does after authenticating. Driving a real upgrade would test the
// library; this tests the routing decision, which is ours.
func subscribeTo(hub *EventHub, mailboxID string, buffer int) *subscriber {
	sub := &subscriber{mailboxID: mailboxID, events: make(chan EventMessage, buffer)}
	hub.add(sub)
	return sub
}

// The check that keeps one mailbox's activity off another's connection.
func TestEventsReachOnlyTheirOwnMailbox(t *testing.T) {
	hub := newTestHub(t, storeWithMail())
	mine := subscribeTo(hub, mailboxOne.String(), 4)
	theirs := subscribeTo(hub, mailboxTwo.String(), 4)

	hub.Publish(port.OutboxEvent{
		EventType:   "EmailReceived",
		AggregateID: mailboxOne,
		Payload:     []byte(`{"uid":7}`),
	})

	select {
	case msg := <-mine.events:
		if msg.Type != "EmailReceived" {
			t.Errorf("got event type %q", msg.Type)
		}
	default:
		t.Error("the mailbox's own event never arrived")
	}

	select {
	case msg := <-theirs.events:
		t.Errorf("another mailbox's event was delivered: %+v", msg)
	default:
	}
}

// A subscriber that has stopped reading must be dropped, never allowed to
// stall the relay that every other connection shares.
func TestASlowSubscriberIsDroppedRatherThanBlocking(t *testing.T) {
	hub := newTestHub(t, storeWithMail())
	// Buffer of one, then filled, so the next publish has nowhere to go.
	stalled := subscribeTo(hub, mailboxOne.String(), 1)
	stalled.events <- EventMessage{Type: "filler"}

	done := make(chan struct{})
	go func() {
		hub.Publish(port.OutboxEvent{
			EventType:   "EmailReceived",
			AggregateID: mailboxOne,
			Payload:     []byte(`{}`),
		})
		close(done)
	}()

	select {
	case <-done:
	case <-context.Background().Done():
		t.Fatal("Publish blocked on a subscriber that was not reading")
	}
}

func TestSubscriberCountFollowsAddAndRemove(t *testing.T) {
	hub := newTestHub(t, storeWithMail())
	if hub.SubscriberCount() != 0 {
		t.Fatalf("a new hub has %d subscribers", hub.SubscriberCount())
	}

	sub := subscribeTo(hub, mailboxOne.String(), 1)
	if hub.SubscriberCount() != 1 {
		t.Errorf("after one subscriber: %d", hub.SubscriberCount())
	}

	hub.remove(sub)
	if hub.SubscriberCount() != 0 {
		t.Errorf("after removing it: %d", hub.SubscriberCount())
	}
}

// Publishing with nobody listening must be a no-op, not a panic: the relay
// runs whether or not a browser happens to be open.
func TestPublishWithNoSubscribersIsHarmless(t *testing.T) {
	hub := newTestHub(t, storeWithMail())
	hub.Publish(port.OutboxEvent{
		EventType:   "EmailReceived",
		AggregateID: uuid.New(),
		Payload:     []byte(`{}`),
	})
}

// --- the upgrade's own authentication ------------------------------------

// The session is verified before the upgrade, so an unauthenticated caller
// gets a plain 401 and never reaches the WebSocket machinery.
func TestEventStreamRefusesAnUnauthenticatedUpgrade(t *testing.T) {
	hub := newTestHub(t, storeWithMail())

	rec := httptest.NewRecorder()
	hub.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an unauthenticated upgrade answered %d, want 401", rec.Code)
	}
}

func TestEventStreamRefusesAnAdminSession(t *testing.T) {
	hub := newTestHub(t, storeWithMail())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req.AddCookie(&http.Cookie{Name: "lm_user_session", Value: adminSurfaceToken(t)})
	rec := httptest.NewRecorder()
	hub.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an admin session reached the event stream: %d", rec.Code)
	}
}

// A valid session for an address this server no longer serves must not open a
// stream either - that is a deleted account whose token has not expired yet.
func TestEventStreamRefusesASessionWithNoMailbox(t *testing.T) {
	store := storeWithMail()
	delete(store.addresses, sessionOwner)
	hub := newTestHub(t, store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req.AddCookie(&http.Cookie{Name: "lm_user_session", Value: interopSession})
	rec := httptest.NewRecorder()
	hub.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a session with no mailbox answered %d, want 401", rec.Code)
	}
}

// The payload has to reach the browser unchanged: the page reads the UID out
// of it to decide what to refresh.
func TestEventCarriesItsPayloadThrough(t *testing.T) {
	hub := newTestHub(t, storeWithMail())
	sub := subscribeTo(hub, mailboxOne.String(), 2)

	hub.Publish(port.OutboxEvent{
		EventType:   "EmailReceived",
		AggregateID: mailboxOne,
		Payload:     []byte(`{"recipient":"user@example.test","uid":42}`),
	})

	msg := <-sub.events
	var payload struct {
		Recipient string `json:"recipient"`
		UID       int    `json:"uid"`
	}
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("payload did not survive: %v", err)
	}
	if payload.UID != 42 || payload.Recipient != "user@example.test" {
		t.Errorf("payload arrived as %+v", payload)
	}
}
