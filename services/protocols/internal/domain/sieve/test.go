package sieve

import (
	"fmt"
	"strings"
)

// test is a condition a rule asks about a message.
type test interface {
	matches(msg Message) bool
}

// headerTest is `header :comparator "Name" "value"`.
type headerTest struct {
	name       string
	value      string
	comparator string
}

func (h headerTest) matches(msg Message) bool {
	for _, actual := range msg.header(h.name) {
		if compare(h.comparator, actual, h.value) {
			return true
		}
	}
	return false
}

type anyOf struct{ tests []test }

func (a anyOf) matches(msg Message) bool {
	for _, t := range a.tests {
		if t.matches(msg) {
			return true
		}
	}
	return false
}

type allOf struct{ tests []test }

func (a allOf) matches(msg Message) bool {
	for _, t := range a.tests {
		if !t.matches(msg) {
			return false
		}
	}
	return len(a.tests) > 0
}

type notTest struct{ inner test }

func (n notTest) matches(msg Message) bool { return !n.inner.matches(msg) }

// compare applies one of Sieve's match types.
//
// All of them ignore case. Sieve's default comparator is
// "i;ascii-casemap", and for mail rules it is also what a person means: a
// subject is the same subject however it was capitalised.
func compare(comparator, actual, want string) bool {
	a := strings.ToLower(strings.TrimSpace(actual))
	w := strings.ToLower(want)
	switch comparator {
	case "is":
		return a == w
	case "matches":
		return wildcardMatch(a, w)
	default: // contains
		return strings.Contains(a, w)
	}
}

// wildcardMatch implements Sieve's :matches, where * is any run of characters
// and ? is exactly one.
//
// Deliberately not a regular expression: in Sieve every other character is a
// literal, so a pattern like "a.b@example.test" must match only a real dot.
// Compiling it as a regex would quietly match "axb@example.test" too - and
// would hand a script author the entire regex grammar, including patterns
// that take exponential time on a crafted subject line.
func wildcardMatch(value, pattern string) bool {
	v, p := []rune(value), []rune(pattern)
	// Iterative backtracking: linear in the common case, and it cannot blow
	// the stack on a long subject the way a recursive version can.
	vi, pi := 0, 0
	star, mark := -1, 0
	for vi < len(v) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == v[vi]):
			vi++
			pi++
		case pi < len(p) && p[pi] == '*':
			star = pi
			mark = vi
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			vi = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// parseTest reads the condition of an if/elsif.
func parseTest(source string) (test, error) {
	source = strings.TrimSpace(source)
	lower := strings.ToLower(source)

	switch {
	case strings.HasPrefix(lower, "not "):
		inner, err := parseTest(source[len("not "):])
		if err != nil {
			return nil, err
		}
		return notTest{inner: inner}, nil

	case strings.HasPrefix(lower, "anyof"), strings.HasPrefix(lower, "allof"):
		inner, err := parseTestList(source[len("anyof"):])
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(lower, "anyof") {
			return anyOf{tests: inner}, nil
		}
		return allOf{tests: inner}, nil

	case strings.HasPrefix(lower, "header"), strings.HasPrefix(lower, "address"):
		// address is treated as header here: the fields a rule asks about -
		// From, To, Cc - carry the address in the header anyway, and matching
		// the whole header is the more forgiving of the two.
		return parseHeaderTest(source)

	case lower == "true":
		return allOf{tests: []test{}}, nil
	}
	return nil, fmt.Errorf("sieve: unsupported test %q", firstWord(source))
}

// parseTestList splits "(a, b, c)" into its tests, respecting quotes and
// nesting so a comma inside a value does not split the list.
func parseTestList(source string) ([]test, error) {
	source = strings.TrimSpace(source)
	source = strings.TrimPrefix(source, "(")
	source = strings.TrimSuffix(strings.TrimSpace(source), ")")

	var parts []string
	var current strings.Builder
	depth, inString := 0, false
	for i := 0; i < len(source); i++ {
		c := source[i]
		switch {
		case inString:
			current.WriteByte(c)
			if c == '\\' && i+1 < len(source) {
				i++
				current.WriteByte(source[i])
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
			current.WriteByte(c)
		case c == '(':
			depth++
			current.WriteByte(c)
		case c == ')':
			depth--
			current.WriteByte(c)
		case c == ',' && depth == 0:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		parts = append(parts, current.String())
	}

	out := make([]test, 0, len(parts))
	for _, part := range parts {
		parsed, err := parseTest(part)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("sieve: empty test list")
	}
	return out, nil
}

func parseHeaderTest(source string) (test, error) {
	comparator := "contains"
	for _, candidate := range []string{"contains", "is", "matches"} {
		if strings.Contains(strings.ToLower(source), ":"+candidate) {
			comparator = candidate
			break
		}
	}
	values := quotedStrings(source)
	if len(values) < 2 {
		return nil, fmt.Errorf("sieve: header test needs a name and a value")
	}
	return headerTest{name: values[0], value: values[1], comparator: comparator}, nil
}
