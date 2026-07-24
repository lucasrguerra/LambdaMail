// Package imappresentation: this file implements IMAP SEARCH (RFC 3501
// section 6.4.4). Criteria matching is a pure function operating on data
// already available via the application layer (FetchMessages, ReadBlob) -
// it intentionally lives here, not in the use case, because imap.
// SearchCriteria is a go-imap/v2 type and the application layer must stay
// framework-free (see this plan's Global Constraints).
package imappresentation

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-message/textproto"
)

func (s *session) Search(numKind imapserver.NumKind, criteria *imap.SearchCriteria, _ *imap.SearchOptions) (*imap.SearchData, error) {
	if s.selectedFolder == nil {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "No mailbox selected"}
	}
	messages, err := s.useCase.FetchMessages(context.Background(), s.selectedFolder.ID)
	if err != nil {
		return nil, toIMAPError(err)
	}

	data := &imap.SearchData{}
	var seqSet imap.SeqSet
	var uidSet imap.UIDSet

	for i, msg := range messages {
		seqNum := uint32(i) + 1
		flags := make([]imap.Flag, len(msg.Flags))
		for j, f := range msg.Flags {
			flags[j] = imap.Flag(f)
		}

		blobID := msg.BlobID
		loadRaw := func() ([]byte, error) {
			return s.useCase.ReadBlob(context.Background(), blobID)
		}

		if !matchCriteria(seqNum, msg.UID, msg.ReceivedAt, msg.SizeBytes, flags, criteria, loadRaw) {
			continue
		}

		seqSet.AddNum(seqNum)
		uidSet.AddNum(imap.UID(msg.UID))
		var num uint32
		if numKind == imapserver.NumKindUID {
			num = msg.UID
		} else {
			num = seqNum
		}
		if data.Min == 0 || num < data.Min {
			data.Min = num
		}
		if num > data.Max {
			data.Max = num
		}
		data.Count++
	}

	if numKind == imapserver.NumKindUID {
		data.All = uidSet
	} else {
		data.All = seqSet
	}
	return data, nil
}

// matchCriteria mirrors go-imap/v2's own imapmemserver reference matching
// logic (imapserver/imapmemserver/message.go's (*message).search), adapted
// to lazily load raw message bytes via loadRaw only when a criterion
// actually needs them (Header/Body/Text/SentSince/SentBefore), since this
// codebase is Postgres-backed with blobs fetched on demand rather than
// always held in memory like imapmemserver's reference message buffer.
func matchCriteria(seqNum, uid uint32, receivedAt time.Time, sizeBytes int64, flags []imap.Flag, criteria *imap.SearchCriteria, loadRaw func() ([]byte, error)) bool {
	for _, seqSet := range criteria.SeqNum {
		if seqNum == 0 || !seqSet.Contains(seqNum) {
			return false
		}
	}
	for _, uidSet := range criteria.UID {
		if !uidSet.Contains(imap.UID(uid)) {
			return false
		}
	}
	if !matchDate(receivedAt, criteria.Since, criteria.Before) {
		return false
	}

	flagSet := make(map[imap.Flag]struct{}, len(flags))
	for _, f := range flags {
		flagSet[canonicalFlag(f)] = struct{}{}
	}
	for _, flag := range criteria.Flag {
		if _, ok := flagSet[canonicalFlag(flag)]; !ok {
			return false
		}
	}
	for _, flag := range criteria.NotFlag {
		if _, ok := flagSet[canonicalFlag(flag)]; ok {
			return false
		}
	}

	if criteria.Larger != 0 && sizeBytes <= criteria.Larger {
		return false
	}
	if criteria.Smaller != 0 && sizeBytes >= criteria.Smaller {
		return false
	}

	needsRaw := len(criteria.Header) > 0 || len(criteria.Body) > 0 || len(criteria.Text) > 0 ||
		!criteria.SentSince.IsZero() || !criteria.SentBefore.IsZero()
	var header textproto.Header
	var raw []byte
	if needsRaw {
		var err error
		raw, err = loadRaw()
		if err != nil {
			return false
		}
		header, err = textproto.ReadHeader(bufio.NewReader(bytes.NewReader(raw)))
		if err != nil {
			return false
		}
	}

	for _, fieldCriteria := range criteria.Header {
		if !matchHeaderField(header, fieldCriteria.Key, fieldCriteria.Value) {
			return false
		}
	}

	if !criteria.SentSince.IsZero() || !criteria.SentBefore.IsZero() {
		t, err := parseDateHeader(header)
		if err != nil || !matchDate(t, criteria.SentSince, criteria.SentBefore) {
			return false
		}
	}

	for _, text := range criteria.Text {
		if !matchEntity(raw, text, true) {
			return false
		}
	}
	for _, body := range criteria.Body {
		if !matchEntity(raw, body, false) {
			return false
		}
	}

	for _, not := range criteria.Not {
		if matchCriteria(seqNum, uid, receivedAt, sizeBytes, flags, &not, loadRaw) {
			return false
		}
	}
	for _, or := range criteria.Or {
		if !matchCriteria(seqNum, uid, receivedAt, sizeBytes, flags, &or[0], loadRaw) &&
			!matchCriteria(seqNum, uid, receivedAt, sizeBytes, flags, &or[1], loadRaw) {
			return false
		}
	}

	return true
}

// canonicalFlag normalizes flag comparisons so e.g. "\Seen" and "\seen" are
// treated as equal, matching IMAP's case-insensitive flag semantics.
func canonicalFlag(f imap.Flag) imap.Flag {
	return imap.Flag(strings.ToLower(string(f)))
}

func matchDate(t, since, before time.Time) bool {
	// RFC 3501 explicitly requires zone-unaware date (not time) comparison.
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if !since.IsZero() && t.Before(since) {
		return false
	}
	if !before.IsZero() && !t.Before(before) {
		return false
	}
	return true
}

func matchHeaderField(header textproto.Header, key, pattern string) bool {
	fields := header.FieldsByKey(key)
	if pattern == "" {
		return fields.Len() > 0
	}
	pattern = strings.ToLower(pattern)
	for fields.Next() {
		if strings.Contains(strings.ToLower(fields.Value()), pattern) {
			return true
		}
	}
	return false
}

// parseDateHeader parses the message's Date: header field.
//
// The real API (verified against github.com/emersion/go-message@installed
// version, and against how go-imap/v2's own imapserver/imapmemserver
// reference implementation does this in message.go's (*message).search:
// `header := mail.Header{msg.reader().Header}` then `header.Date()`) is
// *not* a type assertion on Header.Fields() - that sketch does not compile,
// since the iterator returned by Fields() has no Date() method. Instead,
// github.com/emersion/go-message/mail.Header wraps a
// github.com/emersion/go-message.Header (which itself wraps
// textproto.Header) and exposes a `Date() (time.Time, error)` method that
// does the real RFC 5322 date parsing. So a textproto.Header must be
// wrapped twice - once into gomessage.Header, once into mail.Header - to
// reach that method.
func parseDateHeader(header textproto.Header) (time.Time, error) {
	h := mail.Header{Header: gomessage.Header{Header: header}}
	return h.Date()
}

// matchEntity re-parses raw as a MIME entity and checks whether pattern
// (case-insensitively) appears in the body (and, if includeHeader is true,
// also the header fields) - mirroring imapmemserver's matchEntity, which
// walks multipart parts recursively via gomessage.Entity rather than doing
// a flat byte search over raw, so that HTML/text alternative parts and
// nested MIME structures are still searched correctly.
func matchEntity(raw []byte, pattern string, includeHeader bool) bool {
	if pattern == "" {
		return true
	}
	entity, err := gomessage.Read(bytes.NewReader(raw))
	if err != nil && entity == nil {
		return false
	}
	return matchEntityRecursive(entity, strings.ToLower(pattern), includeHeader)
}

func matchEntityRecursive(e *gomessage.Entity, patternLower string, includeHeader bool) bool {
	if e == nil {
		return false
	}
	if includeHeader && matchHeaderFieldsLower(e.Header.Fields(), patternLower) {
		return true
	}

	if mr := e.MultipartReader(); mr != nil {
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			if matchEntityRecursive(part, patternLower, includeHeader) {
				return true
			}
		}
		return false
	}

	body, err := io.ReadAll(e.Body)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(body)), patternLower)
}

func matchHeaderFieldsLower(fields gomessage.HeaderFields, patternLower string) bool {
	for fields.Next() {
		v, _ := fields.Text()
		if strings.Contains(strings.ToLower(v), patternLower) {
			return true
		}
	}
	return false
}
