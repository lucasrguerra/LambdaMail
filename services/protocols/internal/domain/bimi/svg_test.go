package bimi

import (
	"fmt"
	"strings"
	"testing"
)

// A logo that satisfies every rule, used as the starting point each case
// then breaks in exactly one way.
const goodLogo = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" version="1.2" baseProfile="tiny-ps" viewBox="0 0 512 512">
  <title>LambdaMail</title>
  <rect width="512" height="512" fill="#1b1b25"/>
  <path d="M128 384 L256 128 L384 384 Z" fill="#9184d9"/>
</svg>`

func rulesOf(vs []Violation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Rule)
	}
	return out
}

func contains(rules []string, want string) bool {
	for _, r := range rules {
		if r == want {
			return true
		}
	}
	return false
}

func TestValidate_AcceptsAConformingLogo(t *testing.T) {
	if vs := Validate([]byte(goodLogo)); len(vs) != 0 {
		t.Errorf("a conforming logo was rejected: %v", vs)
	}
}

func TestValidate_RequiresTheProfileReceiversLookFor(t *testing.T) {
	cases := map[string]struct{ from, to, rule string }{
		// Without the profile the document is a plain SVG, and a receiver
		// ignores it without saying so.
		"missing baseProfile": {`baseProfile="tiny-ps" `, "", "baseProfile"},
		"wrong baseProfile":   {`baseProfile="tiny-ps"`, `baseProfile="full"`, "baseProfile"},
		"missing version":     {`version="1.2" `, "", "version"},
		"wrong version":       {`version="1.2"`, `version="1.1"`, "version"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			logo := strings.Replace(goodLogo, tc.from, tc.to, 1)
			if !contains(rulesOf(Validate([]byte(logo))), tc.rule) {
				t.Errorf("no %s violation reported", tc.rule)
			}
		})
	}
}

// Every client crops or letterboxes a non-square mark differently, so the
// specification requires the viewBox itself be square.
func TestValidate_RequiresASquareViewBox(t *testing.T) {
	for _, viewBox := range []string{"0 0 512 256", "0 0 100 200", "0 0 512", "", "0 0 0 0", "0 0 a b"} {
		t.Run(viewBox, func(t *testing.T) {
			logo := strings.Replace(goodLogo, `viewBox="0 0 512 512"`, fmt.Sprintf(`viewBox=%q`, viewBox), 1)
			if !contains(rulesOf(Validate([]byte(logo))), "viewBox") {
				t.Errorf("viewBox %q was accepted", viewBox)
			}
		})
	}

	// Commas are a legal separator and must not be read as a malformed box.
	commas := strings.Replace(goodLogo, `viewBox="0 0 512 512"`, `viewBox="0,0,512,512"`, 1)
	if contains(rulesOf(Validate([]byte(commas))), "viewBox") {
		t.Error("a comma-separated square viewBox was rejected")
	}
}

func TestValidate_RequiresExactlyOneTitle(t *testing.T) {
	none := strings.Replace(goodLogo, "<title>LambdaMail</title>", "", 1)
	if !contains(rulesOf(Validate([]byte(none))), "title") {
		t.Error("a logo with no title was accepted")
	}

	two := strings.Replace(goodLogo, "<title>LambdaMail</title>", "<title>A</title><title>B</title>", 1)
	if !contains(rulesOf(Validate([]byte(two))), "title") {
		t.Error("a logo with two titles was accepted")
	}

	empty := strings.Replace(goodLogo, "<title>LambdaMail</title>", "<title>  </title>", 1)
	if !contains(rulesOf(Validate([]byte(empty))), "title") {
		t.Error("an empty title was accepted")
	}
}

// The logo is fetched and rendered by other people's mail clients. Anything
// active or anything that reaches out over the network has no business in it.
func TestValidate_RefusesAnythingActiveOrExternal(t *testing.T) {
	cases := map[string]struct{ insert, rule string }{
		"script element": {`<script>alert(1)</script>`, "forbiddenElement"},
		"link":           {`<a href="https://example.test">x</a>`, "forbiddenElement"},
		"raster image":   {`<image href="https://example.test/p.png"/>`, "forbiddenElement"},
		"foreign object": {`<foreignObject><b>x</b></foreignObject>`, "forbiddenElement"},
		"animation":      {`<animate attributeName="x" to="9"/>`, "forbiddenElement"},
		"event handler":  {`<rect width="1" height="1" onload="alert(1)"/>`, "script"},
		"external href":  {`<rect width="1" height="1" xlink:href="https://example.test/x"/>`, "externalReference"},
		"data uri":       {`<rect width="1" height="1" href="data:image/png;base64,AAAA"/>`, "externalReference"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			logo := strings.Replace(goodLogo, "</svg>", tc.insert+"</svg>", 1)
			if !contains(rulesOf(Validate([]byte(logo))), tc.rule) {
				t.Errorf("%s was accepted: %v", name, Validate([]byte(logo)))
			}
		})
	}
}

func TestValidate_RefusesWhatCannotBeReadAtAll(t *testing.T) {
	if !contains(rulesOf(Validate([]byte(""))), "empty") {
		t.Error("an empty file was accepted")
	}
	if !contains(rulesOf(Validate([]byte("<svg><unclosed>"))), "xml") {
		t.Error("malformed XML was accepted")
	}
	// A PNG renamed to .svg is the likeliest wrong upload of all.
	if len(Validate([]byte{0x89, 'P', 'N', 'G'})) == 0 {
		t.Error("a PNG was accepted as a logo")
	}
}

func TestValidate_RefusesALogoTooBigToBeFetched(t *testing.T) {
	padding := strings.Repeat(" ", MaxLogoBytes)
	logo := strings.Replace(goodLogo, "</svg>", "<!--"+padding+"--></svg>", 1)
	if !contains(rulesOf(Validate([]byte(logo))), "size") {
		t.Error("an oversized logo was accepted")
	}
}

// One rule at a time, against a receiver that shows nothing either way, is a
// miserable way to learn these - so every violation is reported at once.
func TestValidate_ReportsEveryViolationAtOnce(t *testing.T) {
	broken := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 20"><script/></svg>`
	rules := rulesOf(Validate([]byte(broken)))
	for _, want := range []string{"baseProfile", "version", "viewBox", "title", "forbiddenElement"} {
		if !contains(rules, want) {
			t.Errorf("missing %s; got %v", want, rules)
		}
	}
}
