package valueobject

import "testing"

func TestProtocolSecurityLevel_StringRoundTrip(t *testing.T) {
	cases := []struct {
		level ProtocolSecurityLevel
		want  string
	}{
		{SecurityOpportunistic, "opportunistic"},
		{SecurityRequired, "required"},
		{SecurityImplicit, "implicit"},
	}
	for _, c := range cases {
		if got := c.level.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}
