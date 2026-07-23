package valueobject

import (
	"errors"
	"strings"
)

// PasswordHash wraps an Argon2id PHC-format hash string (see PLAN.md section 9, mailboxes.password_hash).
type PasswordHash struct {
	phc string
}

var (
	ErrPasswordHashEmpty       = errors.New("password hash: must not be empty")
	ErrPasswordHashWrongScheme = errors.New("password hash: only argon2id PHC strings are accepted")
)

// NewPasswordHash validates that phc is a well-formed argon2id PHC string.
func NewPasswordHash(phc string) (PasswordHash, error) {
	if phc == "" {
		return PasswordHash{}, ErrPasswordHashEmpty
	}
	if !strings.HasPrefix(phc, "$argon2id$") {
		return PasswordHash{}, ErrPasswordHashWrongScheme
	}
	return PasswordHash{phc: phc}, nil
}

func (p PasswordHash) String() string { return p.phc }
