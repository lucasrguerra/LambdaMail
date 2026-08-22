package sieve

import (
	"fmt"
	"strconv"
	"strings"
)

// statement is one thing a script does.
type statement interface {
	// stopped reports whether evaluation must end here.
	stopped(msg Message, out *Outcome) bool
}

// --- statements ----------------------------------------------------------

type conditional struct {
	branches []branch
	fallback []statement
}

type branch struct {
	test test
	body []statement
}

func (c conditional) stopped(msg Message, out *Outcome) bool {
	for _, b := range c.branches {
		if b.test.matches(msg) {
			return run(b.body, msg, out)
		}
	}
	return run(c.fallback, msg, out)
}

type fileInto struct{ folder string }

func (f fileInto) stopped(_ Message, out *Outcome) bool {
	// The first rule to file the message decides. Letting a later rule
	// overwrite it would make two rules silently disagree about one message.
	if out.Folder == "" {
		out.Folder = f.folder
	}
	return false
}

type discard struct{}

func (discard) stopped(_ Message, out *Outcome) bool {
	out.Discard = true
	return false
}

type addFlag struct{ flag string }

func (a addFlag) stopped(_ Message, out *Outcome) bool {
	for _, existing := range out.Flags {
		if existing == a.flag {
			return false
		}
	}
	out.Flags = append(out.Flags, a.flag)
	return false
}

type keep struct{}

func (keep) stopped(_ Message, _ *Outcome) bool { return false }

type stop struct{}

func (stop) stopped(_ Message, _ *Outcome) bool { return true }

type vacation struct {
	subject string
	body    string
}

func (v vacation) stopped(msg Message, out *Outcome) bool {
	if !shouldAutoReply(msg) {
		return false
	}
	out.Vacation = &VacationReply{Subject: v.subject, Body: v.body}
	return false
}

// --- parsing -------------------------------------------------------------

// splitStatements flattens a script into statement-sized pieces, dropping
// comments and blank lines and keeping braces as their own tokens so blocks
// can be found without a full tokeniser.
func splitStatements(source string) []string {
	var out []string
	var current strings.Builder
	inString := false

	flush := func() {
		text := strings.TrimSpace(current.String())
		if text != "" {
			out = append(out, text)
		}
		current.Reset()
	}

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
		case c == '#':
			// Comment to end of line.
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case c == '{' || c == '}':
			flush()
			out = append(out, string(c))
		case c == ';':
			flush()
		default:
			current.WriteByte(c)
		}
	}
	flush()
	return out
}

// parseBlock reads statements until the block ends.
func parseBlock(lines []string, i int) ([]statement, int, error) {
	var out []statement
	for i < len(lines) {
		line := lines[i]
		if line == "}" {
			return out, i, nil
		}
		st, next, err := parseStatement(lines, i)
		if err != nil {
			return nil, 0, err
		}
		if st != nil {
			out = append(out, st)
		}
		i = next
	}
	return out, i, nil
}

func parseStatement(lines []string, i int) (statement, int, error) {
	line := lines[i]
	head := strings.ToLower(firstWord(line))

	switch head {
	case "require":
		// Capabilities are declarative; what this engine supports is decided
		// by what it can parse below, so the line is accepted and skipped.
		return nil, i + 1, nil
	case "if":
		return parseConditional(lines, i)
	case "fileinto":
		folder, err := singleString(line, "fileinto")
		if err != nil {
			return nil, 0, err
		}
		return fileInto{folder: folder}, i + 1, nil
	case "addflag", "setflag":
		flag, err := singleString(line, head)
		if err != nil {
			return nil, 0, err
		}
		return addFlag{flag: flag}, i + 1, nil
	case "discard":
		return discard{}, i + 1, nil
	case "keep":
		return keep{}, i + 1, nil
	case "stop":
		return stop{}, i + 1, nil
	case "vacation":
		v, err := parseVacation(line)
		if err != nil {
			return nil, 0, err
		}
		return v, i + 1, nil
	case "":
		return nil, i + 1, nil
	default:
		// Refused rather than ignored: a rule the user wrote that quietly does
		// nothing is worse than one that reports it cannot run.
		return nil, 0, fmt.Errorf("sieve: unsupported statement %q", firstWord(line))
	}
}

func parseConditional(lines []string, i int) (statement, int, error) {
	cond := conditional{}

	for i < len(lines) {
		line := lines[i]
		head := strings.ToLower(firstWord(line))
		if head != "if" && head != "elsif" && head != "else" {
			break
		}

		if head == "else" {
			body, next, err := readBraced(lines, i+1)
			if err != nil {
				return nil, 0, err
			}
			cond.fallback = body
			i = next
			break
		}

		testSource := strings.TrimSpace(line[len(head):])
		parsedTest, err := parseTest(testSource)
		if err != nil {
			return nil, 0, err
		}
		body, next, err := readBraced(lines, i+1)
		if err != nil {
			return nil, 0, err
		}
		cond.branches = append(cond.branches, branch{test: parsedTest, body: body})
		i = next

		if len(cond.branches) > maxRules {
			return nil, 0, fmt.Errorf("sieve: too many branches")
		}
	}

	if len(cond.branches) == 0 {
		return nil, 0, fmt.Errorf("sieve: if without a test")
	}
	return cond, i, nil
}

// readBraced consumes "{ ... }" starting at i.
func readBraced(lines []string, i int) ([]statement, int, error) {
	if i >= len(lines) || lines[i] != "{" {
		return nil, 0, fmt.Errorf("sieve: expected a block")
	}
	body, next, err := parseBlock(lines, i+1)
	if err != nil {
		return nil, 0, err
	}
	if next >= len(lines) || lines[next] != "}" {
		return nil, 0, fmt.Errorf("sieve: unclosed block")
	}
	return body, next + 1, nil
}

func firstWord(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == ' ' || line[i] == '\t' || line[i] == '(' {
			return line[:i]
		}
	}
	return line
}

// quotedStrings pulls every "..." out of a fragment, honouring escapes.
func quotedStrings(fragment string) []string {
	var out []string
	for i := 0; i < len(fragment); i++ {
		if fragment[i] != '"' {
			continue
		}
		var b strings.Builder
		i++
		for i < len(fragment) && fragment[i] != '"' {
			if fragment[i] == '\\' && i+1 < len(fragment) {
				i++
				// \\ and \" are the escapes Sieve defines; anything else keeps
				// the backslash, which is what makes "\\Flagged" survive.
				switch fragment[i] {
				case '"', '\\':
					b.WriteByte(fragment[i])
				default:
					b.WriteByte('\\')
					b.WriteByte(fragment[i])
				}
			} else {
				b.WriteByte(fragment[i])
			}
			i++
		}
		out = append(out, b.String())
	}
	return out
}

func singleString(line, keyword string) (string, error) {
	values := quotedStrings(strings.TrimSpace(line[len(keyword):]))
	if len(values) != 1 {
		return "", fmt.Errorf("sieve: %s needs exactly one quoted value", keyword)
	}
	return values[0], nil
}

func parseVacation(line string) (statement, error) {
	rest := strings.TrimSpace(line[len("vacation"):])
	values := quotedStrings(rest)
	if len(values) == 0 {
		return nil, fmt.Errorf("sieve: vacation needs a message")
	}
	// ":subject" takes the first quoted value; the reason is the last.
	if strings.Contains(strings.ToLower(rest), ":subject") && len(values) >= 2 {
		return vacation{subject: values[0], body: values[len(values)-1]}, nil
	}
	return vacation{body: values[len(values)-1]}, nil
}

var _ = strconv.Quote
