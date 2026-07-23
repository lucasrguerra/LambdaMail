package entity

import "testing"

func TestOutboundJob_RecordAttempt_AllowsUpToMax(t *testing.T) {
	j := NewOutboundJob(3)
	for i := 0; i < 3; i++ {
		if err := j.RecordAttempt(); err != nil {
			t.Fatalf("attempt %d: unexpected error: %v", i+1, err)
		}
	}
	if j.Attempt() != 3 {
		t.Errorf("Attempt() = %d, want 3", j.Attempt())
	}
}

func TestOutboundJob_RecordAttempt_RejectsBeyondMax(t *testing.T) {
	j := NewOutboundJob(1)
	if err := j.RecordAttempt(); err != nil {
		t.Fatalf("unexpected error on first attempt: %v", err)
	}
	if err := j.RecordAttempt(); err == nil {
		t.Fatal("expected error: attempt must never exceed max_attempts (PLAN.md section 3), got nil")
	}
}

func TestOutboundJob_NextBackoff_IsMonotonicPerRetryTable(t *testing.T) {
	// Backoff schedule from PLAN.md section 6.3: attempts 1-4 map to
	// immediate, +5m, +15m, +1h.
	j := NewOutboundJob(8)
	var prev int64 = -1
	for i := 0; i < 4; i++ {
		j.RecordAttempt()
		d := j.NextBackoff()
		if int64(d) < prev {
			t.Errorf("attempt %d: backoff %v is not >= previous backoff", i+1, d)
		}
		prev = int64(d)
	}
}
