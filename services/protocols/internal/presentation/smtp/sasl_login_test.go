package smtppresentation

import (
	"errors"
	"testing"
)

// The LOGIN exchange prompts for the username, then the password, and only
// then authenticates.
func TestLoginServer_PromptsThenAuthenticates(t *testing.T) {
	var gotUser, gotPass string
	server := newLoginServer(func(username, password string) error {
		gotUser, gotPass = username, password
		return nil
	})

	challenge, done, err := server.Next(nil)
	if err != nil || done || string(challenge) != "Username:" {
		t.Fatalf("first step: challenge=%q done=%v err=%v", challenge, done, err)
	}

	challenge, done, err = server.Next([]byte("user@example.test"))
	if err != nil || done || string(challenge) != "Password:" {
		t.Fatalf("second step: challenge=%q done=%v err=%v", challenge, done, err)
	}

	_, done, err = server.Next([]byte("secret"))
	if err != nil || !done {
		t.Fatalf("third step: done=%v err=%v", done, err)
	}
	if gotUser != "user@example.test" || gotPass != "secret" {
		t.Errorf("credentials = %q/%q", gotUser, gotPass)
	}
}

// A client that sends the username as its initial response skips the first
// prompt.
func TestLoginServer_AcceptsInitialResponse(t *testing.T) {
	server := newLoginServer(func(string, string) error { return nil })

	challenge, done, err := server.Next([]byte("user@example.test"))
	if err != nil || done || string(challenge) != "Password:" {
		t.Fatalf("challenge=%q done=%v err=%v", challenge, done, err)
	}
}

func TestLoginServer_PropagatesAuthFailure(t *testing.T) {
	authErr := errors.New("bad credentials")
	server := newLoginServer(func(string, string) error { return authErr })

	server.Next(nil)
	server.Next([]byte("user@example.test"))
	if _, done, err := server.Next([]byte("wrong")); err == nil || done {
		t.Fatalf("expected the failure to surface: done=%v err=%v", done, err)
	}
}

func TestLoginServer_RefusesExtraSteps(t *testing.T) {
	server := newLoginServer(func(string, string) error { return nil })
	server.Next(nil)
	server.Next([]byte("user@example.test"))
	server.Next([]byte("secret"))

	if _, _, err := server.Next([]byte("extra")); err == nil {
		t.Fatal("expected an error after the exchange finished")
	}
}
