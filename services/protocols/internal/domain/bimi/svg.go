// Package bimi validates the logo a domain publishes for BIMI.
//
// BIMI is the only mechanism by which an external mail client shows an image
// for a sender it has never met. It is per domain, not per mailbox, and the
// receiving side is strict: a logo that is not SVG Tiny Portable/Secure is
// ignored without explanation, so the checks that matter are made here rather
// than discovered from a mailbox that quietly shows nothing.
package bimi

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
)

// MaxLogoBytes is the ceiling the BIMI specification sets. Receivers fetch
// this over the network for every sender they evaluate.
const MaxLogoBytes = 32 * 1024

// Violation is one reason a logo would be rejected.
type Violation struct {
	Rule   string
	Detail string
}

func (v Violation) String() string { return fmt.Sprintf("%s: %s", v.Rule, v.Detail) }

// Elements that must not appear. Script and event handlers make the document
// active; external references make it fetch; raster images defeat the point of
// a vector mark and are how a tracking pixel would arrive.
var forbiddenElements = []string{"script", "a", "image", "foreignObject", "use", "animate", "animateTransform", "animateMotion", "set"}

var (
	svgOpenRe      = regexp.MustCompile(`(?is)<svg\b[^>]*>`)
	attrRe         = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*"([^"]*)"|([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*'([^']*)'`)
	onEventRe      = regexp.MustCompile(`(?is)\son[a-z]+\s*=`)
	externalRefRe  = regexp.MustCompile(`(?is)(href|xlink:href|src)\s*=\s*["']\s*(https?:|//|data:)`)
	titleElementRe = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title>`)
)

// Validate reports every reason the logo would not be accepted.
//
// All of them, not the first: an operator fixing one rule at a time against a
// receiver that simply shows nothing is the worst way to learn these.
func Validate(payload []byte) []Violation {
	var out []Violation
	add := func(rule, detail string) { out = append(out, Violation{Rule: rule, Detail: detail}) }

	if len(payload) == 0 {
		return []Violation{{Rule: "empty", Detail: "the file is empty"}}
	}
	if len(payload) > MaxLogoBytes {
		add("size", fmt.Sprintf("%d bytes; the limit is %d", len(payload), MaxLogoBytes))
	}

	// Parseable at all: a receiver will not repair a broken document.
	if err := xml.Unmarshal(payload, new(struct {
		XMLName xml.Name
	})); err != nil {
		return append(out, Violation{Rule: "xml", Detail: "the file is not well-formed XML"})
	}

	text := string(payload)
	open := svgOpenRe.FindString(text)
	if open == "" {
		return append(out, Violation{Rule: "svg", Detail: "no <svg> root element"})
	}

	attrs := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(open, -1) {
		if m[1] != "" {
			attrs[strings.ToLower(m[1])] = m[2]
		} else {
			attrs[strings.ToLower(m[3])] = m[4]
		}
	}

	// The profile a receiver looks for. Without it the document is a plain
	// SVG and is ignored.
	if got := attrs["baseprofile"]; !strings.EqualFold(got, "tiny-ps") {
		add("baseProfile", fmt.Sprintf("must be \"tiny-ps\", found %q", got))
	}
	if got := attrs["version"]; got != "1.2" {
		add("version", fmt.Sprintf("must be \"1.2\", found %q", got))
	}

	// A non-square mark is cropped or letterboxed differently by every client,
	// so the specification requires the viewBox itself be square.
	if err := checkSquareViewBox(attrs["viewbox"]); err != nil {
		add("viewBox", err.Error())
	}

	// x and y are meaningful only when the mark is placed in another document,
	// and the specification forbids them on the root.
	for _, forbidden := range []string{"x", "y"} {
		if _, present := attrs[forbidden]; present {
			add("rootAttributes", fmt.Sprintf("the root <svg> must not carry %q", forbidden))
		}
	}

	// Exactly one title, and it is what a screen reader announces.
	titles := titleElementRe.FindAllStringSubmatch(text, -1)
	switch {
	case len(titles) == 0:
		add("title", "a <title> element is required")
	case len(titles) > 1:
		add("title", fmt.Sprintf("found %d <title> elements; there must be exactly one", len(titles)))
	case strings.TrimSpace(titles[0][1]) == "":
		add("title", "the <title> element is empty")
	}

	lower := strings.ToLower(text)
	for _, name := range forbiddenElements {
		if strings.Contains(lower, "<"+strings.ToLower(name)) {
			add("forbiddenElement", fmt.Sprintf("<%s> is not allowed", name))
		}
	}
	if onEventRe.MatchString(text) {
		add("script", "event handler attributes are not allowed")
	}
	if externalRefRe.MatchString(text) {
		add("externalReference", "the logo must not reference anything outside itself")
	}

	return out
}

// checkSquareViewBox verifies the mark occupies a square.
func checkSquareViewBox(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("a viewBox is required")
	}
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\n' })
	if len(fields) != 4 {
		return fmt.Errorf("must be four numbers, found %q", value)
	}

	var width, height float64
	if _, err := fmt.Sscanf(fields[2], "%g", &width); err != nil {
		return fmt.Errorf("width %q is not a number", fields[2])
	}
	if _, err := fmt.Sscanf(fields[3], "%g", &height); err != nil {
		return fmt.Errorf("height %q is not a number", fields[3])
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("width and height must be positive")
	}
	if width != height {
		return fmt.Errorf("must be square; found %g by %g", width, height)
	}
	return nil
}
