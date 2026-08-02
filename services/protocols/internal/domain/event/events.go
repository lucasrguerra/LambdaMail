// Package event contains the domain events listed in PLAN.md section 3.
// They are persisted through the transactional outbox (section 9.4), never
// published directly on a bus from inside a write transaction.
package event

import "github.com/google/uuid"

// DomainEvent is implemented by every domain event.
type DomainEvent interface {
	Type() string
	AggregateID() uuid.UUID
}

type EmailReceived struct{ MessageAggregateID uuid.UUID }

func (e EmailReceived) Type() string           { return "EmailReceived" }
func (e EmailReceived) AggregateID() uuid.UUID { return e.MessageAggregateID }

type EmailAccepted struct{ MessageAggregateID uuid.UUID }

func (e EmailAccepted) Type() string           { return "EmailAccepted" }
func (e EmailAccepted) AggregateID() uuid.UUID { return e.MessageAggregateID }

type EmailDelivered struct{ JobAggregateID uuid.UUID }

func (e EmailDelivered) Type() string           { return "EmailDelivered" }
func (e EmailDelivered) AggregateID() uuid.UUID { return e.JobAggregateID }

type EmailDeferred struct{ JobAggregateID uuid.UUID }

func (e EmailDeferred) Type() string           { return "EmailDeferred" }
func (e EmailDeferred) AggregateID() uuid.UUID { return e.JobAggregateID }

type EmailBounced struct{ JobAggregateID uuid.UUID }

func (e EmailBounced) Type() string           { return "EmailBounced" }
func (e EmailBounced) AggregateID() uuid.UUID { return e.JobAggregateID }

type MailboxQuotaWarning struct{ MailboxAggregateID uuid.UUID }

func (e MailboxQuotaWarning) Type() string           { return "MailboxQuotaWarning" }
func (e MailboxQuotaWarning) AggregateID() uuid.UUID { return e.MailboxAggregateID }

type MailboxQuotaExceeded struct{ MailboxAggregateID uuid.UUID }

func (e MailboxQuotaExceeded) Type() string           { return "MailboxQuotaExceeded" }
func (e MailboxQuotaExceeded) AggregateID() uuid.UUID { return e.MailboxAggregateID }

type SpamDetected struct{ MessageAggregateID uuid.UUID }

func (e SpamDetected) Type() string           { return "SpamDetected" }
func (e SpamDetected) AggregateID() uuid.UUID { return e.MessageAggregateID }

type VirusDetected struct{ MessageAggregateID uuid.UUID }

func (e VirusDetected) Type() string           { return "VirusDetected" }
func (e VirusDetected) AggregateID() uuid.UUID { return e.MessageAggregateID }

type DkimRotationStarted struct{ DomainAggregateID uuid.UUID }

func (e DkimRotationStarted) Type() string           { return "DkimRotationStarted" }
func (e DkimRotationStarted) AggregateID() uuid.UUID { return e.DomainAggregateID }

type DkimRotationCompleted struct{ DomainAggregateID uuid.UUID }

func (e DkimRotationCompleted) Type() string           { return "DkimRotationCompleted" }
func (e DkimRotationCompleted) AggregateID() uuid.UUID { return e.DomainAggregateID }

type DnsRecordDrifted struct{ DomainAggregateID uuid.UUID }

func (e DnsRecordDrifted) Type() string           { return "DnsRecordDrifted" }
func (e DnsRecordDrifted) AggregateID() uuid.UUID { return e.DomainAggregateID }

type CertificateRotated struct{ HostAggregateID uuid.UUID }

func (e CertificateRotated) Type() string           { return "CertificateRotated" }
func (e CertificateRotated) AggregateID() uuid.UUID { return e.HostAggregateID }

type AuthenticationFailed struct{ MailboxAggregateID uuid.UUID }

func (e AuthenticationFailed) Type() string           { return "AuthenticationFailed" }
func (e AuthenticationFailed) AggregateID() uuid.UUID { return e.MailboxAggregateID }

type RateLimitTriggered struct{ SubjectAggregateID uuid.UUID }

func (e RateLimitTriggered) Type() string           { return "RateLimitTriggered" }
func (e RateLimitTriggered) AggregateID() uuid.UUID { return e.SubjectAggregateID }
