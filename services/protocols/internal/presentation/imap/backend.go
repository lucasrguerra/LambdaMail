package imappresentation

import (
	"bufio"
	"bytes"
	"context"
	"errors"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-message/textproto"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
)

// session adapts imapserver.Session to ImapSessionUseCase.
type session struct {
	useCase        *usecase.ImapSessionUseCase
	mailboxID      string
	selectedFolder *port.ImapFolderRecord
}

// NewSession is the imapserver.Options.NewSession callback.
func NewSession(_ *imapserver.Conn, uc *usecase.ImapSessionUseCase) (imapserver.Session, *imapserver.GreetingData, error) {
	return &session{useCase: uc}, &imapserver.GreetingData{}, nil
}

func toIMAPError(err error) error {
	switch {
	case errors.Is(err, usecase.ErrAuthFailed):
		return imapserver.ErrAuthFailed
	case errors.Is(err, usecase.ErrNoSuchFolder):
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "No such mailbox"}
	default:
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Internal error"}
	}
}

func (s *session) Close() error { return nil }

func (s *session) Login(username, password string) error {
	mailboxID, err := s.useCase.Login(context.Background(), username, password)
	if err != nil {
		return toIMAPError(err)
	}
	s.mailboxID = mailboxID
	return nil
}

func (s *session) Select(mailbox string, _ *imap.SelectOptions) (*imap.SelectData, error) {
	rec, err := s.useCase.SelectFolder(context.Background(), s.mailboxID, mailbox)
	if err != nil {
		return nil, toIMAPError(err)
	}
	s.selectedFolder = rec
	return &imap.SelectData{
		NumMessages: rec.NumMessages,
		UIDNext:     imap.UID(rec.UIDNext),
		UIDValidity: rec.UIDValidity,
	}, nil
}

func (s *session) Unselect() error {
	s.selectedFolder = nil
	return nil
}

func (s *session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	rec, err := s.useCase.SelectFolder(context.Background(), s.mailboxID, mailbox)
	if err != nil {
		return nil, toIMAPError(err)
	}
	data := &imap.StatusData{Mailbox: mailbox}
	if options.NumMessages {
		n := rec.NumMessages
		data.NumMessages = &n
	}
	if options.UIDNext {
		data.UIDNext = imap.UID(rec.UIDNext)
	}
	if options.UIDValidity {
		data.UIDValidity = rec.UIDValidity
	}
	return data, nil
}

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	recs, err := s.useCase.ListFolders(context.Background(), s.mailboxID)
	if err != nil {
		return toIMAPError(err)
	}
	for _, rec := range matchFolders(recs, ref, patterns) {
		if err := w.WriteList(&imap.ListData{Mailbox: rec.Name, Delim: '/'}); err != nil {
			return err
		}
	}
	return nil
}

// matchFolders filters recs down to those whose name matches at least one of
// patterns (relative to ref), using the same wildcard semantics as the LIST
// command (imapserver.MatchList: '*' matches any characters including the
// hierarchy delimiter, '%' matches any characters except the delimiter).
// Extracted from List so the matching/filtering logic can be unit-tested
// directly, since *imapserver.ListWriter cannot be constructed outside a
// live imapserver.Conn.
func matchFolders(recs []port.ImapFolderRecord, ref string, patterns []string) []port.ImapFolderRecord {
	var out []port.ImapFolderRecord
	for _, rec := range recs {
		for _, pattern := range patterns {
			if imapserver.MatchList(rec.Name, '/', ref, pattern) {
				out = append(out, rec)
				break
			}
		}
	}
	return out
}

// numSetIter walks every message in the selected folder in UID order,
// yielding its 1-based sequence number alongside it, and reports whether
// numSet contains that message (matching imapmemserver's own reference
// pattern of iterating known messages and calling NumSet.Contains, rather
// than trying to resolve "*" ranges ourselves).
func (s *session) numSetIter(numSet imap.NumSet, isUID bool, messages []port.MessageRecord, f func(seqNum uint32, msg port.MessageRecord)) {
	for i, msg := range messages {
		seqNum := uint32(i) + 1
		var contains bool
		switch set := numSet.(type) {
		case imap.SeqSet:
			contains = set.Contains(seqNum)
		case imap.UIDSet:
			contains = set.Contains(imap.UID(msg.UID))
		}
		if contains {
			f(seqNum, msg)
		}
	}
}

func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	if s.selectedFolder == nil {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "No mailbox selected"}
	}
	messages, err := s.useCase.FetchMessages(context.Background(), s.selectedFolder.ID)
	if err != nil {
		return toIMAPError(err)
	}

	var fetchErr error
	s.numSetIter(numSet, options.UID, messages, func(seqNum uint32, msg port.MessageRecord) {
		if fetchErr != nil {
			return
		}
		respWriter := w.CreateMessage(seqNum)
		fetchErr = s.writeFetchResponse(respWriter, msg, options)
	})
	return fetchErr
}

func (s *session) writeFetchResponse(w *imapserver.FetchResponseWriter, msg port.MessageRecord, options *imap.FetchOptions) error {
	w.WriteUID(imap.UID(msg.UID))
	if options.Flags {
		flags := make([]imap.Flag, len(msg.Flags))
		for i, f := range msg.Flags {
			flags[i] = imap.Flag(f)
		}
		w.WriteFlags(flags)
	}
	if options.InternalDate {
		w.WriteInternalDate(msg.ReceivedAt)
	}
	if options.RFC822Size {
		w.WriteRFC822Size(msg.SizeBytes)
	}

	needsRaw := options.Envelope || options.BodyStructure != nil || len(options.BodySection) > 0 || len(options.BinarySection) > 0
	if needsRaw {
		raw, err := s.useCase.ReadBlob(context.Background(), msg.BlobID)
		if err != nil {
			return err
		}
		if options.Envelope {
			header, err := textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
			if err != nil {
				return err
			}
			w.WriteEnvelope(imapserver.ExtractEnvelope(header))
		}
		if options.BodyStructure != nil {
			w.WriteBodyStructure(imapserver.ExtractBodyStructure(bytes.NewReader(raw)))
		}
		for _, bs := range options.BodySection {
			buf := imapserver.ExtractBodySection(bytes.NewReader(raw), bs)
			wc := w.WriteBodySection(bs, int64(len(buf)))
			_, writeErr := wc.Write(buf)
			closeErr := wc.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return w.Close()
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if s.selectedFolder == nil {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "No mailbox selected"}
	}
	var op port.FlagOp
	switch flags.Op {
	case imap.StoreFlagsAdd:
		op = port.FlagOpAdd
	case imap.StoreFlagsDel:
		op = port.FlagOpDel
	case imap.StoreFlagsSet:
		op = port.FlagOpSet
	}
	flagNames := make([]string, len(flags.Flags))
	for i, f := range flags.Flags {
		flagNames[i] = string(f)
	}

	messages, err := s.useCase.FetchMessages(context.Background(), s.selectedFolder.ID)
	if err != nil {
		return toIMAPError(err)
	}

	// Resolve numSet against the folder's real message list (same pattern as
	// Fetch), rather than pulling raw values via NumSet.Nums(): dynamic
	// ranges like "1:*" cannot be resolved without a concrete candidate to
	// test against, and Nums() returns (nil, false) for them. This also
	// naturally supports plain (non-UID) STORE via the SeqSet case.
	var storeErr error
	s.numSetIter(numSet, true, messages, func(_ uint32, msg port.MessageRecord) {
		if storeErr != nil {
			return
		}
		if err := s.useCase.SetFlags(context.Background(), s.selectedFolder.ID, msg.UID, op, flagNames); err != nil {
			storeErr = toIMAPError(err)
		}
	})
	if storeErr != nil {
		return storeErr
	}
	if !flags.Silent {
		return s.Fetch(w, numSet, &imap.FetchOptions{Flags: true, UID: true})
	}
	return nil
}
