package httppresentation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// WebSession is the claim set the auth service puts in its session tokens.
// The two services share only this shape and the signing secret; the tokens
// themselves are issued in one place and verified in both.
type WebSession struct {
	Subject      string `json:"sub"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	DomainID     string `json:"domainId"`
	Surface      string `json:"surface"`
	Audience     string `json:"aud"`
	MfaSatisfied bool   `json:"mfaSatisfied"`
	Purpose      string `json:"purpose"`
	IssuedAt     int64  `json:"iat"`
	ExpiresAt    int64  `json:"exp"`
}

var (
	ErrTokenMalformed = errors.New("web session: token is not a three-part JWT")
	ErrBadSignature   = errors.New("web session: signature does not verify")
	ErrTokenExpired   = errors.New("web session: token has expired")
	ErrNotASession    = errors.New("web session: token is not a session token")
	ErrWrongAudience  = errors.New("web session: token was issued for a different surface")
)

// WebSessionVerifier validates the tokens minted by the auth service.
//
// The secret is shared rather than the tokens being re-checked over HTTP: a
// round trip to the auth service on every mail request would put a second
// network hop in the read path for no extra safety, since both services
// already trust the same secret to say who the user is.
type WebSessionVerifier struct {
	secret []byte
	now    func() time.Time
}

func NewWebSessionVerifier(secret string) *WebSessionVerifier {
	return &WebSessionVerifier{secret: []byte(secret), now: time.Now}
}

// Verify checks the signature, the expiry, and that this is a real session
// rather than the short-lived challenge issued between the password and the
// second factor.
func (v *WebSessionVerifier) Verify(token string) (*WebSession, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrTokenMalformed
	}

	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	// Compared with hmac.Equal so a wrong signature costs the same time as a
	// right one, whatever the mismatch position.
	if !hmac.Equal([]byte(parts[2]), []byte(expected)) {
		return nil, ErrBadSignature
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrTokenMalformed
	}

	var session WebSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, ErrTokenMalformed
	}

	if session.ExpiresAt > 0 && v.now().Unix() >= session.ExpiresAt {
		return nil, ErrTokenExpired
	}
	if session.Purpose != "session" {
		return nil, ErrNotASession
	}

	return &session, nil
}

// RequireSurface additionally pins the token to one surface, which is what
// keeps a /user token out of an admin-only endpoint (PLAN.md section 14.1).
func (v *WebSessionVerifier) RequireSurface(token, surface string) (*WebSession, error) {
	session, err := v.Verify(token)
	if err != nil {
		return nil, err
	}
	if session.Surface != surface || session.Audience != "lambdamail:"+surface {
		return nil, ErrWrongAudience
	}
	return session, nil
}
