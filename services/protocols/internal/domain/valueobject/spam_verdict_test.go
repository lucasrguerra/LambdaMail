package valueobject

import "testing"

func TestSpamVerdictFromScore_MatchesThresholds(t *testing.T) {
	// Thresholds from PLAN.md section 10.1.
	cases := []struct {
		score float64
		want  SpamVerdict
	}{
		{0, SpamHam},
		{3.9, SpamHam},
		{4, SpamProbable},
		{8.9, SpamProbable},
		{9, SpamReject},   // section 10.1: 9-15 greylists/defers, modeled here as reject-eligible
		{15.1, SpamReject},
	}
	for _, c := range cases {
		if got := SpamVerdictFromScore(c.score); got != c.want {
			t.Errorf("SpamVerdictFromScore(%v) = %v, want %v", c.score, got, c.want)
		}
	}
}
