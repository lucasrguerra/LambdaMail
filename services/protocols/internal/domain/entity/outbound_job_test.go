package entity

import (
	"testing"
	"time"
)

func TestCalculateNextBackoff_SchedulesCorrectDelays(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(5 * 24 * time.Hour)

	// Attempt 2 -> +5 min
	next, delayDsn, perm := CalculateNextBackoff(now, 2, expiresAt)
	if perm || delayDsn || next.Sub(now) < 4*time.Minute {
		t.Errorf("attempt 2 backoff mismatch: next=%v, delayDsn=%v, perm=%v", next, delayDsn, perm)
	}

	// Attempt 4 -> +1 hr + delayDsn=true
	next, delayDsn, perm = CalculateNextBackoff(now, 4, expiresAt)
	if perm || !delayDsn || next.Sub(now) < 55*time.Minute {
		t.Errorf("attempt 4 backoff mismatch: next=%v, delayDsn=%v, perm=%v", next, delayDsn, perm)
	}

	// Expired -> permanent failure
	expiredTime := now.Add(-1 * time.Hour)
	_, _, perm = CalculateNextBackoff(now, 5, expiredTime)
	if !perm {
		t.Errorf("expected permanent failure for expired job")
	}
}
