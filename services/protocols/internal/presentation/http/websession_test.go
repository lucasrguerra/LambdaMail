package httppresentation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// These tokens were minted by the auth service itself (services/auth,
// session.ts) under the secret below. They are the real wire format: if the
// two implementations ever drift, every mail request from the webmail starts
// failing and this test says so first.
const (
	interopSecret    = "shared-secret-for-interop-test"
	interopSession   = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJtYi0xIiwiZW1haWwiOiJ1c2VyQGV4YW1wbGUudGVzdCIsInJvbGUiOiJVU0VSIiwiZG9tYWluSWQiOiJkb20tMSIsInN1cmZhY2UiOiJ1c2VyIiwiYXVkIjoibGFtYmRhbWFpbDp1c2VyIiwibWZhU2F0aXNmaWVkIjp0cnVlLCJtZmFTYXRpc2ZpZWRBdCI6MTc4NTY3NDI0MzYzMiwicHVycG9zZSI6InNlc3Npb24iLCJpYXQiOjE3ODU2NzQyNDMsImV4cCI6MTc4NTcwMzA0M30.vrXM2T9HKfHDOThmx0LD_n05jfsr4iaKYpZrKTBa5NM"
	interopChallenge = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJtYi0xIiwiZW1haWwiOiJ1c2VyQGV4YW1wbGUudGVzdCIsInJvbGUiOiJVU0VSIiwiZG9tYWluSWQiOiJkb20tMSIsInN1cmZhY2UiOiJ1c2VyIiwiYXVkIjoibGFtYmRhbWFpbDp1c2VyIiwibWZhU2F0aXNmaWVkIjpmYWxzZSwicHVycG9zZSI6Im1mYV9jaGFsbGVuZ2UiLCJpYXQiOjE3ODU2NzQyNDMsImV4cCI6MTc4NTY3NDU0M30.7OqmI9A1L9Efbt6AX4lqojZZ8IKuWi-6oe_VJNgJAxM"
	// The captured tokens carry a fixed validity window. Verification is
	// pinned to an instant inside it, so these tests assert on the signature
	// and the claims rather than quietly turning red once the tokens age out.
	interopIssuedAt = 1785674243
)

func testVerifier(secret string) *WebSessionVerifier {
	v := NewWebSessionVerifier(secret)
	v.now = func() time.Time { return time.Unix(interopIssuedAt+60, 0) }
	return v
}

func TestWebSessionVerifier_AcceptsTokenIssuedByTheAuthService(t *testing.T) {
	session, err := testVerifier(interopSecret).Verify(interopSession)
	if err != nil {
		t.Fatalf("a token minted by the auth service was rejected: %v", err)
	}
	if session.Email != "user@example.test" {
		t.Errorf("Email = %q, want user@example.test", session.Email)
	}
	if session.Surface != "user" || session.Audience != "lambdamail:user" {
		t.Errorf("surface/aud = %q/%q, want user/lambdamail:user", session.Surface, session.Audience)
	}
}

// The challenge token is issued after the password but before the second
// factor. Accepting it here would let the mail API be opened without MFA.
func TestWebSessionVerifier_RejectsChallengeToken(t *testing.T) {
	_, err := testVerifier(interopSecret).Verify(interopChallenge)
	if !errors.Is(err, ErrNotASession) {
		t.Fatalf("error = %v, want ErrNotASession", err)
	}
}

func TestWebSessionVerifier_RejectsWrongSecret(t *testing.T) {
	_, err := testVerifier("not-the-secret").Verify(interopSession)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("error = %v, want ErrBadSignature", err)
	}
}

// A tampered payload must not survive: this is the check that stops a user
// from promoting their own token to the admin surface.
func TestWebSessionVerifier_RejectsTamperedPayload(t *testing.T) {
	tampered := interopSession[:len(interopSession)-6] + "AAAAAA"
	if _, err := testVerifier(interopSecret).Verify(tampered); err == nil {
		t.Fatal("a tampered token verified")
	}
}

func TestWebSessionVerifier_RequireSurfaceRejectsOtherSurface(t *testing.T) {
	_, err := testVerifier(interopSecret).RequireSurface(interopSession, "admin")
	if !errors.Is(err, ErrWrongAudience) {
		t.Fatalf("error = %v, want ErrWrongAudience", err)
	}
	if _, err := testVerifier(interopSecret).RequireSurface(interopSession, "user"); err != nil {
		t.Fatalf("the matching surface was rejected: %v", err)
	}
}

func TestWebSessionVerifier_RejectsMalformed(t *testing.T) {
	v := testVerifier(interopSecret)
	for _, token := range []string{"", "a.b", "a.b.c.d", "not-a-token"} {
		if _, err := v.Verify(token); err == nil {
			t.Errorf("token %q verified", token)
		}
	}
}

// Expiry still has to bite: pinning the clock must not disable the check.
func TestWebSessionVerifier_RejectsExpiredToken(t *testing.T) {
	v := NewWebSessionVerifier(interopSecret)
	v.now = func() time.Time { return time.Unix(interopIssuedAt+9*60*60, 0) }
	if _, err := v.Verify(interopSession); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("error = %v, want ErrTokenExpired", err)
	}
}

// mintSession signs a session token the way the auth service does, so a test
// can build a claim set the captured tokens do not cover - another surface, an
// expired window, a different address.
//
// The captured tokens above stay the interop check: this only produces the
// variations, and produces them through the same algorithm.
func mintSession(t *testing.T, claims WebSession) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)

	mac := hmac.New(sha256.New, []byte(interopSecret))
	mac.Write([]byte(header + "." + payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return header + "." + payload + "." + signature
}

// The minted tokens have to verify, or every test built on them is asserting
// against a signature this server would reject for the wrong reason.
func TestMintedSessionVerifiesLikeARealOne(t *testing.T) {
	token := mintSession(t, WebSession{
		Subject: "mb-1", Email: "user@example.test", Surface: "user",
		Audience: "lambdamail:user", Purpose: "session", MfaSatisfied: true,
		IssuedAt: interopIssuedAt, ExpiresAt: interopIssuedAt + 3600,
	})

	session, err := testVerifier(interopSecret).RequireSurface(token, "user")
	if err != nil {
		t.Fatalf("a locally minted session was rejected: %v", err)
	}
	if session.Email != "user@example.test" {
		t.Errorf("Email = %q", session.Email)
	}
}
