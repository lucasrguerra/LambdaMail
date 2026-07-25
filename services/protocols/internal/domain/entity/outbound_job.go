package entity

import (
	"math/rand"
	"time"

	"github.com/google/uuid"
)

type OutboundJobStatus string

const (
	OutboundJobStatusQueued    OutboundJobStatus = "QUEUED"
	OutboundJobStatusSending   OutboundJobStatus = "SENDING"
	OutboundJobStatusDeferred  OutboundJobStatus = "DEFERRED"
	OutboundJobStatusDelivered OutboundJobStatus = "DELIVERED"
	OutboundJobStatusBounced   OutboundJobStatus = "BOUNCED"
	OutboundJobStatusCancelled OutboundJobStatus = "CANCELLED"
)

type OutboundJob struct {
	ID                uuid.UUID
	MailboxID         *uuid.UUID
	BlobID            uuid.UUID
	EnvelopeFrom      string
	EnvelopeTo        string
	DestinationDomain string
	Status            OutboundJobStatus
	Attempt           int
	NextAttemptAt     time.Time
	ExpiresAt         time.Time
	LastSmtpCode      string
	LastError         string
	TlsPolicyUsed     string
	DelayDsnSent      bool
	CreatedAt         time.Time
}

// CalculateNextBackoff computes the next retry time and DSN requirements based on attempt count.
func CalculateNextBackoff(now time.Time, attempt int, expiresAt time.Time) (nextAttemptAt time.Time, sendDelayDsn bool, permanentFailure bool) {
	if !expiresAt.IsZero() && (now.After(expiresAt) || now.Equal(expiresAt)) {
		return now, false, true
	}

	var baseDelay time.Duration

	switch attempt {
	case 1:
		baseDelay = 0
	case 2:
		baseDelay = 5 * time.Minute
	case 3:
		baseDelay = 15 * time.Minute
	case 4:
		baseDelay = 1 * time.Hour
		sendDelayDsn = true
	case 5:
		baseDelay = 2 * time.Hour
	case 6:
		baseDelay = 4 * time.Hour
	case 7:
		baseDelay = 8 * time.Hour
	default:
		baseDelay = 12 * time.Hour
	}

	if baseDelay > 0 && attempt >= 5 {
		// Apply ±20% jitter
		jitterFactor := 0.8 + rand.Float64()*0.4
		baseDelay = time.Duration(float64(baseDelay) * jitterFactor)
	}

	nextAttemptAt = now.Add(baseDelay)
	if !expiresAt.IsZero() && nextAttemptAt.After(expiresAt) {
		return expiresAt, sendDelayDsn, true
	}

	return nextAttemptAt, sendDelayDsn, false
}
