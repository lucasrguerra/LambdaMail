package event

import (
	"testing"

	"github.com/google/uuid"
)

func TestEmailReceived_ImplementsDomainEvent(t *testing.T) {
	id := uuid.New()
	e := EmailReceived{MessageAggregateID: id}
	if e.Type() != "EmailReceived" {
		t.Errorf("Type() = %q, want %q", e.Type(), "EmailReceived")
	}
	if e.AggregateID() != id {
		t.Errorf("AggregateID() = %v, want %v", e.AggregateID(), id)
	}
}

func TestEmailBounced_ImplementsDomainEvent(t *testing.T) {
	var _ DomainEvent = EmailBounced{}
}
