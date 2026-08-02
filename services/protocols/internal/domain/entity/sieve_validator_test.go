package entity

import (
	"errors"
	"testing"
)

func TestValidateSieveScript_ValidScripts(t *testing.T) {
	validScripts := []string{
		`require ["fileinto", "reject"];
if header :is "Subject" "Spam" {
    fileinto "Junk";
    stop;
}`,
		`# Simple keep script
keep;`,
		`/* Multi-line comment
   testing Sieve script */
if size :over 1M {
    discard;
} else {
    keep;
}`,
	}

	for i, s := range validScripts {
		if err := ValidateSieveScript(s); err != nil {
			t.Errorf("script %d failed: %v", i, err)
		}
	}
}

func TestValidateSieveScript_InvalidScripts(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr error
	}{
		{"empty", "", ErrEmptyScript},
		{"whitespace", "   \n\t  ", ErrEmptyScript},
		{"unmatched opening brace", `if true { keep;`, ErrUnmatchedBrace},
		{"unmatched closing brace", `if true { keep; }}`, ErrUnmatchedBrace},
		{"unmatched bracket", `require ["fileinto";`, ErrUnmatchedBracket},
		{"unterminated string", `fileinto "Junk;`, ErrUnterminatedQuote},
		{"unterminated comment", `/* comment without end`, ErrUnterminatedComment},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSieveScript(tt.script)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
