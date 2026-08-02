package entity

import (
	"testing"
	"time"
)

// PLAN.md section 6.3 defines the wait *before the next* attempt: after
// attempt 1 fails the next try is +5 min, after attempt 3 it is +1 h together
// with the delay DSN, and from the fifth try on the delay carries +/-20%
// jitter. A zero delay after the first failure would hammer the destination in
// a tight loop.
func TestCalculateNextBackoff_FollowsRetryTable(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(5 * 24 * time.Hour)

	cases := []struct {
		failedAttempt int
		wantMin       time.Duration
		wantMax       time.Duration
		wantDelayDsn  bool
	}{
		{1, 5 * time.Minute, 5 * time.Minute, false},
		{2, 15 * time.Minute, 15 * time.Minute, false},
		{3, time.Hour, time.Hour, true},
		{4, 96 * time.Minute, 144 * time.Minute, false},   // 2 h +/- 20%
		{5, 192 * time.Minute, 288 * time.Minute, false},  // 4 h +/- 20%
		{6, 384 * time.Minute, 576 * time.Minute, false},  // 8 h +/- 20%
		{7, 576 * time.Minute, 864 * time.Minute, false},  // 12 h +/- 20%
		{20, 576 * time.Minute, 864 * time.Minute, false}, // steady 12 h
	}

	for _, tc := range cases {
		next, delayDsn, perm := CalculateNextBackoff(now, tc.failedAttempt, expiresAt)
		if perm {
			t.Errorf("attempt %d: unexpected permanent failure", tc.failedAttempt)
			continue
		}
		got := next.Sub(now)
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("attempt %d: delay = %v, want between %v and %v", tc.failedAttempt, got, tc.wantMin, tc.wantMax)
		}
		if delayDsn != tc.wantDelayDsn {
			t.Errorf("attempt %d: delayDsn = %v, want %v", tc.failedAttempt, delayDsn, tc.wantDelayDsn)
		}
	}
}

func TestCalculateNextBackoff_ExpiredJobIsPermanent(t *testing.T) {
	now := time.Now()
	if _, _, perm := CalculateNextBackoff(now, 5, now.Add(-time.Hour)); !perm {
		t.Error("expected permanent failure for an expired job")
	}
}
