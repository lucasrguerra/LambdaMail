package valueobject

import (
	"errors"
	"strings"
)

// MessageID is an RFC 5322 Message-ID, always wrapped in angle brackets.
type MessageID struct {
	raw string
}

var (
	ErrMessageIDEmpty       = errors.New("message id: must not be empty")
	ErrMessageIDNotBracketed = errors.New("message id: must be wrapped in angle brackets")
)

func NewMessageID(raw string) (MessageID, error) {
	if raw == "" {
		return MessageID{}, ErrMessageIDEmpty
	}
	if !strings.HasPrefix(raw, "<") || !strings.HasSuffix(raw, ">") {
		return MessageID{}, ErrMessageIDNotBracketed
	}
	return MessageID{raw: raw}, nil
}

func (m MessageID) String() string { return m.raw }
