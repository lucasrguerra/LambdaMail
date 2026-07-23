package valueobject

// ProtocolSecurityLevel is the effective TLS requirement for a listener (see PLAN.md section 4).
type ProtocolSecurityLevel int

const (
	SecurityOpportunistic ProtocolSecurityLevel = iota // STARTTLS offered, plaintext fallback allowed (port 25 only)
	SecurityRequired                                   // STARTTLS mandatory before AUTH (587/143/110/4190)
	SecurityImplicit                                   // TLS from the first byte (465/993/995)
)

func (l ProtocolSecurityLevel) String() string {
	switch l {
	case SecurityRequired:
		return "required"
	case SecurityImplicit:
		return "implicit"
	default:
		return "opportunistic"
	}
}
