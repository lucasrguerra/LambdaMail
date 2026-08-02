package entity

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrEmptyScript         = errors.New("sieve script cannot be empty")
	ErrUnmatchedBrace      = errors.New("unmatched curly brace in sieve script")
	ErrUnmatchedBracket    = errors.New("unmatched square bracket in sieve script")
	ErrUnterminatedQuote   = errors.New("unterminated string literal in sieve script")
	ErrUnterminatedComment = errors.New("unterminated comment block in sieve script")
)

// ValidateSieveScript performs structural syntax validation of an RFC 5228 Sieve script.
func ValidateSieveScript(script string) error {
	trimmed := strings.TrimSpace(script)
	if trimmed == "" {
		return ErrEmptyScript
	}

	var braceCount int
	var bracketCount int
	inSingleQuote := false
	inMultiComment := false
	inString := false

	runes := []rune(script)
	n := len(runes)

	for i := 0; i < n; i++ {
		r := runes[i]

		// Handle multi-line comment /* ... */
		if inMultiComment {
			if r == '*' && i+1 < n && runes[i+1] == '/' {
				inMultiComment = false
				i++
			}
			continue
		}

		// Handle string literal "..."
		if inString {
			if r == '\\' {
				i++ // skip escaped char
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}

		// Handle single line comment # ...
		if inSingleQuote {
			if r == '\n' {
				inSingleQuote = false
			}
			continue
		}

		// Detect start of comment /*
		if r == '/' && i+1 < n && runes[i+1] == '*' {
			inMultiComment = true
			i++
			continue
		}

		// Detect start of comment #
		if r == '#' {
			inSingleQuote = true
			continue
		}

		// Detect start of string "
		if r == '"' {
			inString = true
			continue
		}

		// Track braces & brackets
		switch r {
		case '{':
			braceCount++
		case '}':
			braceCount--
			if braceCount < 0 {
				return ErrUnmatchedBrace
			}
		case '[':
			bracketCount++
		case ']':
			bracketCount--
			if bracketCount < 0 {
				return ErrUnmatchedBracket
			}
		}
	}

	if inMultiComment {
		return ErrUnterminatedComment
	}
	if inString {
		return ErrUnterminatedQuote
	}
	if braceCount != 0 {
		return fmt.Errorf("%w: balance is %d", ErrUnmatchedBrace, braceCount)
	}
	if bracketCount != 0 {
		return fmt.Errorf("%w: balance is %d", ErrUnmatchedBracket, bracketCount)
	}

	return nil
}
