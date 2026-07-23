// Package valueobject contains the domain's immutable, self-validating value objects.
// Zero external imports: this is layer 1 of Clean Architecture (see PLAN.md section 3).
package valueobject

import (
	"errors"
	"strings"
)

// EmailAddress is an RFC 5322 mailbox address, normalized with a lowercased domain.
type EmailAddress struct {
	localPart string
	domain    string
}

var (
	ErrEmailMissingAtSign  = errors.New("email address: missing '@' separator")
	ErrEmailEmptyLocalPart = errors.New("email address: local part must not be empty")
	ErrEmailEmptyDomain    = errors.New("email address: domain must not be empty")
	ErrEmailLocalPartTooLong = errors.New("email address: local part exceeds 64 octets (RFC 5321 4.5.3.1.1)")
)

// NewEmailAddress validates and normalizes raw into an EmailAddress.
func NewEmailAddress(raw string) (EmailAddress, error) {
	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return EmailAddress{}, ErrEmailMissingAtSign
	}
	local, domain := raw[:at], raw[at+1:]
	if local == "" {
		return EmailAddress{}, ErrEmailEmptyLocalPart
	}
	if len(local) > 64 {
		return EmailAddress{}, ErrEmailLocalPartTooLong
	}
	if domain == "" {
		return EmailAddress{}, ErrEmailEmptyDomain
	}
	return EmailAddress{localPart: local, domain: strings.ToLower(domain)}, nil
}

func (e EmailAddress) LocalPart() string { return e.localPart }
func (e EmailAddress) Domain() string    { return e.domain }
func (e EmailAddress) String() string    { return e.localPart + "@" + e.domain }
