package valueobject

import (
	"strings"
	"testing"
)

func TestMtaStsPolicy_Format(t *testing.T) {
	policy := NewMtaStsPolicy(MtaStsModeEnforce, []string{"mail.example.test"}, 604800)
	formatted := policy.Format()

	if !strings.Contains(formatted, "version: STSv1") {
		t.Errorf("missing version: %s", formatted)
	}
	if !strings.Contains(formatted, "mode: enforce") {
		t.Errorf("missing mode: %s", formatted)
	}
	if !strings.Contains(formatted, "mx: mail.example.test") {
		t.Errorf("missing mx: %s", formatted)
	}
	if !strings.Contains(formatted, "max_age: 604800") {
		t.Errorf("missing max_age: %s", formatted)
	}
}
