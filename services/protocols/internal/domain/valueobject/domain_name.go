package valueobject

import (
	"errors"
	"strings"
)

// DomainName is an RFC 1035 fully-qualified domain name, normalized to lowercase.
type DomainName struct {
	value string
}

var (
	ErrDomainNameEmpty         = errors.New("domain name: must not be empty")
	ErrDomainNameNotFQDN       = errors.New("domain name: must contain at least one dot (not a FQDN)")
	ErrDomainNameInvalidLabel  = errors.New("domain name: label must not start or end with a hyphen")
)

// NewDomainName validates and normalizes raw into a DomainName.
func NewDomainName(raw string) (DomainName, error) {
	if raw == "" {
		return DomainName{}, ErrDomainNameEmpty
	}
	lower := strings.ToLower(raw)
	labels := strings.Split(lower, ".")
	if len(labels) < 2 {
		return DomainName{}, ErrDomainNameNotFQDN
	}
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return DomainName{}, ErrDomainNameInvalidLabel
		}
	}
	return DomainName{value: lower}, nil
}

func (d DomainName) String() string { return d.value }

// IsPunycode reports whether the domain name is IDNA/punycode-encoded.
// It returns true if any label of the domain starts with the ASCII Compatible Encoding prefix "xn--".
func (d DomainName) IsPunycode() bool {
	labels := strings.Split(d.value, ".")
	for _, label := range labels {
		if strings.HasPrefix(label, "xn--") {
			return true
		}
	}
	return false
}
