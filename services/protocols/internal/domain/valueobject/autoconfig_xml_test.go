package valueobject

import (
	"strings"
	"testing"
)

func TestBuildThunderbirdAutoconfigXML(t *testing.T) {
	xml := BuildThunderbirdAutoconfigXML("example.test", "mail.example.test")
	if !strings.Contains(xml, "<hostname>mail.example.test</hostname>") {
		t.Errorf("missing mail host: %s", xml)
	}
	if !strings.Contains(xml, "<port>993</port>") {
		t.Errorf("missing port 993: %s", xml)
	}
}

func TestBuildOutlookAutodiscoverXML(t *testing.T) {
	xml := BuildOutlookAutodiscoverXML("example.test", "mail.example.test", "user@example.test")
	if !strings.Contains(xml, "<Server>mail.example.test</Server>") {
		t.Errorf("missing Server: %s", xml)
	}
	if !strings.Contains(xml, "<LoginName>user@example.test</LoginName>") {
		t.Errorf("missing LoginName: %s", xml)
	}
}
