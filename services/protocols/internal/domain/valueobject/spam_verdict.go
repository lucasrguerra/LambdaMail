package valueobject

// SpamVerdict classifies a message by Rspamd score (thresholds from PLAN.md section 10.1).
type SpamVerdict int

const (
	SpamHam SpamVerdict = iota
	SpamProbable
	SpamReject
)

// SpamVerdictFromScore maps a Rspamd score to a verdict per the thresholds table in section 10.1.
func SpamVerdictFromScore(score float64) SpamVerdict {
	switch {
	case score >= 9:
		return SpamReject
	case score >= 4:
		return SpamProbable
	default:
		return SpamHam
	}
}
