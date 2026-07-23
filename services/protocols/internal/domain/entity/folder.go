package entity

import "github.com/google/uuid"

// Folder is an IMAP folder entity. uidNext is strictly monotonic (RFC 3501 section 2.3.1.1),
// enforced per PLAN.md section 3.
type Folder struct {
	mailboxID uuid.UUID
	name      string
	uidNext   int64
}

func NewFolder(mailboxID uuid.UUID, name string) *Folder {
	return &Folder{mailboxID: mailboxID, name: name, uidNext: 1}
}

func (f *Folder) Name() string { return f.name }

// NextUID allocates and returns the next UID, then advances the counter.
func (f *Folder) NextUID() int64 {
	uid := f.uidNext
	f.uidNext++
	return uid
}
