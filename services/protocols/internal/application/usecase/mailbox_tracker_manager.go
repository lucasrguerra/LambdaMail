package usecase

import (
	"sync"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// MailboxTrackerManager maintains imapserver.MailboxTracker instances per folder ID.
// This allows active IMAP sessions to receive real-time notifications when emails arrive,
// flags change, or messages are expunged (IDLE / RFC 2177).
type MailboxTrackerManager struct {
	mu       sync.RWMutex
	trackers map[string]*imapserver.MailboxTracker
}

func NewMailboxTrackerManager() *MailboxTrackerManager {
	return &MailboxTrackerManager{
		trackers: make(map[string]*imapserver.MailboxTracker),
	}
}

// GetTracker retrieves or initializes the MailboxTracker for folderID.
func (m *MailboxTrackerManager) GetTracker(folderID string, initialNumMessages uint32) *imapserver.MailboxTracker {
	m.mu.Lock()
	defer m.mu.Unlock()

	tracker, ok := m.trackers[folderID]
	if !ok {
		tracker = imapserver.NewMailboxTracker(initialNumMessages)
		m.trackers[folderID] = tracker
	}
	return tracker
}

// NotifyNumMessages informs all sessions watching folderID of a new EXISTS count.
func (m *MailboxTrackerManager) NotifyNumMessages(folderID string, n uint32) {
	m.mu.RLock()
	tracker, ok := m.trackers[folderID]
	m.mu.RUnlock()

	if ok {
		tracker.QueueNumMessages(n)
	}
}

// NotifyMessageFlags informs sessions watching folderID of updated flags on a message.
func (m *MailboxTrackerManager) NotifyMessageFlags(folderID string, seqNum uint32, uid uint32, flags []string, source *imapserver.SessionTracker) {
	m.mu.RLock()
	tracker, ok := m.trackers[folderID]
	m.mu.RUnlock()

	if ok {
		imapFlags := make([]imap.Flag, len(flags))
		for i, f := range flags {
			imapFlags[i] = imap.Flag(f)
		}
		tracker.QueueMessageFlags(seqNum, imap.UID(uid), imapFlags, source)
	}
}

// NotifyExpunge informs sessions watching folderID that seqNum was expunged.
func (m *MailboxTrackerManager) NotifyExpunge(folderID string, seqNum uint32) {
	m.mu.RLock()
	tracker, ok := m.trackers[folderID]
	m.mu.RUnlock()

	if ok {
		tracker.QueueExpunge(seqNum)
	}
}
