package entity

import (
	"errors"
	"time"
)

// OutboundJob is a delivery unit with retry state (PLAN.md section 3 and 6.3).
type OutboundJob struct {
	attempt     int
	maxAttempts int
}

var ErrOutboundJobMaxAttemptsExceeded = errors.New("outbound job: attempt count would exceed max_attempts")

func NewOutboundJob(maxAttempts int) *OutboundJob {
	return &OutboundJob{maxAttempts: maxAttempts}
}

func (j *OutboundJob) Attempt() int { return j.attempt }

// RecordAttempt enforces "attempt <= max_attempts" (PLAN.md section 3).
func (j *OutboundJob) RecordAttempt() error {
	if j.attempt >= j.maxAttempts {
		return ErrOutboundJobMaxAttemptsExceeded
	}
	j.attempt++
	return nil
}

// retrySchedule mirrors the accumulated-wait table in PLAN.md section 6.3.
var retrySchedule = []time.Duration{
	0,
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	2 * time.Hour,
	4 * time.Hour,
	8 * time.Hour,
	12 * time.Hour,
}

// NextBackoff returns the wait before the next attempt, per the schedule in section 6.3.
// Attempts beyond the table repeat the last (12h) step, matching the "9+: every 12h" rule.
func (j *OutboundJob) NextBackoff() time.Duration {
	idx := j.attempt
	if idx >= len(retrySchedule) {
		idx = len(retrySchedule) - 1
	}
	return retrySchedule[idx]
}
