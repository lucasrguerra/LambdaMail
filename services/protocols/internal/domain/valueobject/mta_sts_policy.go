package valueobject

import (
	"fmt"
	"strings"
)

type MtaStsMode string

const (
	MtaStsModeTesting MtaStsMode = "testing"
	MtaStsModeEnforce MtaStsMode = "enforce"
	MtaStsModeNone    MtaStsMode = "none"
)

type MtaStsPolicy struct {
	Version string
	Mode    MtaStsMode
	MX      []string
	MaxAge  int
}

func NewMtaStsPolicy(mode MtaStsMode, mx []string, maxAgeSeconds int) MtaStsPolicy {
	if maxAgeSeconds <= 0 {
		maxAgeSeconds = 604800 // default 7 days
	}
	return MtaStsPolicy{
		Version: "STSv1",
		Mode:    mode,
		MX:      mx,
		MaxAge:  maxAgeSeconds,
	}
}

func (p MtaStsPolicy) Format() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("version: %s\r\n", p.Version))
	sb.WriteString(fmt.Sprintf("mode: %s\r\n", p.Mode))
	for _, mxHost := range p.MX {
		sb.WriteString(fmt.Sprintf("mx: %s\r\n", mxHost))
	}
	sb.WriteString(fmt.Sprintf("max_age: %d\r\n", p.MaxAge))
	return sb.String()
}
