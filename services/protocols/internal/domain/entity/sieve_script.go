package entity

import "github.com/google/uuid"

// SieveScript represents a user's Sieve filtering script entity.
type SieveScript struct {
	ID        uuid.UUID
	MailboxID uuid.UUID
	Name      string
	Script    string
	IsActive  bool
}
