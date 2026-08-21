package usecase

import (
	"bytes"
	"context"
	"io"
	"log"
	"strings"

	"github.com/emersion/go-message"
)

// ReportKind says what a delivered message is, decided by the address it was
// sent to.
type ReportKind string

const (
	// ReportKindNone is ordinary mail, which this use case leaves alone.
	ReportKindNone   ReportKind = ""
	ReportKindDmarc  ReportKind = "dmarc"
	ReportKindTlsRpt ReportKind = "tlsrpt"
)

// reportLocalParts maps the mailbox a report is addressed to onto what it
// contains.
//
// The address is the only trustworthy signal. The subject and the attachment
// filename are both chosen by the sender, so keying on either would let anyone
// who can send this server a message write rows into the report tables. These
// local parts exist because EnsureSystemAliases creates them for every domain.
var reportLocalParts = map[string]ReportKind{
	"dmarc":  ReportKindDmarc,
	"tlsrpt": ReportKindTlsRpt,
}

// ReportsFolderName is where an ingested report is filed.
//
// Not the inbox: these are machine-to-machine documents that arrive daily from
// every large receiver, and nobody reads the attachment by hand. Not discarded
// either - keeping the original bytes is what makes a parser fix re-appliable
// to reports already received.
const ReportsFolderName = "Reports"

// IngestDeliveredReportsUseCase reads DMARC and TLS-RPT reports out of the
// mail that carries them.
//
// The parsers, the tables and the HTTP ingest endpoints all existed already,
// but nothing connected inbound mail to them: reports were filed in the
// admin's inbox as attachments nobody could read, and the report tables stayed
// empty however many arrived.
type IngestDeliveredReportsUseCase struct {
	reports *IngestReportsUseCase
}

func NewIngestDeliveredReportsUseCase(reports *IngestReportsUseCase) *IngestDeliveredReportsUseCase {
	return &IngestDeliveredReportsUseCase{reports: reports}
}

// Ingest parses any report the message carries and reports what kind of
// message it was, so the caller can file it somewhere other than the inbox.
//
// It never returns an error for a bad report. A malformed payload is the
// reporter's mistake and a storage failure is this server's, and neither is a
// reason to answer the SMTP transaction with a failure: the reporter would
// retry the same message on a schedule, forever, and no retry would ever help.
// Failures are logged and the message is still delivered, which also leaves
// the original bytes on disk to re-ingest once the cause is fixed.
func (uc *IngestDeliveredReportsUseCase) Ingest(
	ctx context.Context, recipients []string, payload []byte,
) (ReportOutcome, error) {
	kind := ReportKindNone
	for _, address := range recipients {
		if found := reportKindForAddress(address); found != ReportKindNone {
			kind = found
			break
		}
	}
	if kind == ReportKindNone || uc.reports == nil {
		return ReportOutcome{}, nil
	}

	// Every part is tried in turn, so the covering note a reporter puts in
	// front of the attachment is simply one that does not parse. Only the
	// last failure is kept, and only reported if no part worked at all:
	// logging each one would file an error for the human-readable part of
	// every report that ingests perfectly well.
	var lastErr error
	for _, candidate := range reportPayloads(payload) {
		var err error
		switch kind {
		case ReportKindDmarc:
			_, err = uc.reports.IngestDmarc(ctx, candidate)
		case ReportKindTlsRpt:
			_, err = uc.reports.IngestTlsRpt(ctx, candidate)
		}
		if err != nil {
			lastErr = err
			continue
		}
		// One report per message, so there is nothing to gain from reading
		// the remaining parts once one has been stored.
		return ReportOutcome{Kind: kind, Stored: true}, nil
	}

	if lastErr != nil {
		// Logged rather than returned: see the note above on why a bad report
		// must not fail the delivery.
		log.Printf("reports: no readable %s report in a message to %v: %v", kind, recipients, lastErr)
	}
	// Recognised by its address but nothing could be parsed out of it. Still a
	// report, so the caller files it with the others rather than leaving it in
	// the inbox.
	return ReportOutcome{Kind: kind}, nil
}

// ReportOutcome says what a message turned out to be and whether anything was
// actually stored from it.
//
// The two are separate because they drive different decisions: Kind decides
// where the message is filed, and Stored is how a caller tells a report it
// parsed from one it merely recognised - which Ingest deliberately does not
// report as an error.
type ReportOutcome struct {
	Kind   ReportKind
	Stored bool
}

// reportKindForAddress classifies one recipient address by its local part.
func reportKindForAddress(address string) ReportKind {
	local, _, found := strings.Cut(address, "@")
	if !found {
		return ReportKindNone
	}
	return reportLocalParts[strings.ToLower(strings.TrimSpace(local))]
}

// reportPayloads returns every part of the message that could be a report,
// decoded.
//
// Every attachment is offered rather than the one whose name looks right:
// filenames vary between reporters and the parsers already reject anything
// that is not a report, so matching on the name would only add a way to miss
// one. The whole body is offered last, for the reporters that send the
// document as the message itself rather than as an attachment.
func reportPayloads(payload []byte) [][]byte {
	entity, err := message.Read(bytes.NewReader(payload))
	if err != nil {
		return [][]byte{payload}
	}

	candidates := collectReportParts(entity, 0)
	return append(candidates, payload)
}

// maxReportParts bounds the walk: a report has one attachment, and a message
// with hundreds of nested parts is not one this server needs to read.
const maxReportParts = 16

// maxReportPartBytes bounds each part. Aggregate reports are XML or JSON in
// the tens of kilobytes even before compression.
const maxReportPartBytes = 32 << 20

func collectReportParts(entity *message.Entity, depth int) [][]byte {
	// Bounded so a deliberately deep MIME tree cannot drive this into a
	// pathological walk.
	if depth > 8 {
		return nil
	}
	if multipart := entity.MultipartReader(); multipart != nil {
		out := [][]byte{}
		for len(out) < maxReportParts {
			part, err := multipart.NextPart()
			if err != nil {
				break
			}
			out = append(out, collectReportParts(part, depth+1)...)
		}
		return out
	}

	// go-message decodes the transfer encoding as the body is read, so what
	// comes back here is the attachment's real bytes rather than its base64.
	body, err := io.ReadAll(io.LimitReader(entity.Body, maxReportPartBytes))
	if err != nil || len(body) == 0 {
		return nil
	}
	return [][]byte{body}
}
