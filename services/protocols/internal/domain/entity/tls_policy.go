package entity

// TLSPolicy is the effective transport security policy for an outbound destination
// (PLAN.md section 3: DANE has precedence over MTA-STS, RFC 8461 section 2).
type TLSPolicy struct {
	daneAvailable   bool
	mtaSTSAvailable bool
}

func NewTLSPolicy(daneAvailable, mtaSTSAvailable bool) TLSPolicy {
	return TLSPolicy{daneAvailable: daneAvailable, mtaSTSAvailable: mtaSTSAvailable}
}

// Effective returns "dane", "mta-sts" or "opportunistic" following the precedence
// rule in PLAN.md section 5 / RFC 8461 section 2.
func (p TLSPolicy) Effective() string {
	switch {
	case p.daneAvailable:
		return "dane"
	case p.mtaSTSAvailable:
		return "mta-sts"
	default:
		return "opportunistic"
	}
}
