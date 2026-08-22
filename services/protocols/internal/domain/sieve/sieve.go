// Package sieve evaluates the mail-filtering rules a mailbox owner writes.
//
// This is a deliberate subset of RFC 5228 plus the vacation extension (RFC
// 5230): the tests and actions the webmail generates and that a hand-written
// script is likely to use. It is not a complete Sieve implementation, and it
// says so by refusing what it does not understand rather than guessing.
//
// It exists because nothing ran these rules at all. ManageSieve stored
// scripts and the settings screen wrote them, but the delivery path never
// consulted either - so a vacation responder that was switched on never
// replied, and a filter that said "file this in Faturas" filed nothing.
package sieve

import (
	"fmt"
	"strings"
)

// maxRules bounds a script. A mailbox's rules are written by hand; thousands
// of them are not a filing policy, they are a way to make every delivery slow.
const maxRules = 500

// Message is the part of an arriving message the rules can see.
type Message struct {
	// Headers are keyed by lowercased name, since header names are
	// case-insensitive and the script may spell one any way it likes.
	Headers map[string][]string
	// Sender is the envelope sender. Empty means a bounce, which nothing may
	// auto-reply to.
	Sender string
	// Recipient is the mailbox this copy is being delivered to.
	Recipient string
}

func (m Message) header(name string) []string {
	if m.Headers == nil {
		return nil
	}
	return m.Headers[strings.ToLower(strings.TrimSpace(name))]
}

// VacationReply is an out-of-office response the script asked for.
type VacationReply struct {
	Subject string
	Body    string
}

// Outcome is what the rules decided about one message.
type Outcome struct {
	// Folder is where to file it, or empty to leave the delivery path's own
	// choice alone.
	Folder string
	// Discard drops the message. It is still accepted over SMTP: refusing it
	// would tell the sender their mail bounced, which is not what a filing
	// rule means.
	Discard bool
	Flags   []string
	// Vacation is the reply to send, or nil.
	Vacation *VacationReply
}

// Script is a parsed set of rules.
type Script struct {
	statements []statement
}

// Parse reads a script, or reports why it cannot.
//
// A script that does not parse must never be treated as an empty one: silently
// ignoring a rule the user wrote is worse than telling them it is broken.
func Parse(source string) (*Script, error) {
	lines := splitStatements(source)
	if len(lines) > maxRules {
		return nil, fmt.Errorf("sieve: script has %d statements, more than the %d allowed", len(lines), maxRules)
	}
	statements, rest, err := parseBlock(lines, 0)
	if err != nil {
		return nil, err
	}
	if rest != len(lines) {
		return nil, fmt.Errorf("sieve: unexpected %q", lines[rest])
	}
	return &Script{statements: statements}, nil
}

// Evaluate applies a parsed script to one message.
func Evaluate(script *Script, msg Message) Outcome {
	out := Outcome{}
	if script == nil {
		return out
	}
	run(script.statements, msg, &out)
	return out
}

// run walks a block, stopping at the first `stop`.
func run(statements []statement, msg Message, out *Outcome) bool {
	for _, st := range statements {
		if st.stopped(msg, out) {
			return true
		}
	}
	return false
}
