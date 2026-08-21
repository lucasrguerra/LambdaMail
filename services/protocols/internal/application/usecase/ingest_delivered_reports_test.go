package usecase

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

// Reports arrive as ordinary mail to the addresses the system aliases
// guarantee exist. Nothing read them: they were filed in the admin's inbox as
// attachments and the parsed tables stayed empty.

type recordingReportRepo struct {
	dmarc  []*entity.DmarcReport
	tlsRpt []*entity.TlsRptReport
	err    error
}

func (r *recordingReportRepo) SaveDmarcReport(_ context.Context, report *entity.DmarcReport) error {
	if r.err != nil {
		return r.err
	}
	r.dmarc = append(r.dmarc, report)
	return nil
}

func (r *recordingReportRepo) SaveTlsRptReport(_ context.Context, report *entity.TlsRptReport) error {
	if r.err != nil {
		return r.err
	}
	r.tlsRpt = append(r.tlsRpt, report)
	return nil
}

// gzipped compresses a payload the way a reporter does before attaching it.
func gzipped(t *testing.T, payload string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// messageWithAttachment builds the shape a reporter actually sends: a short
// human-readable part plus the report as a base64 attachment.
func messageWithAttachment(filename, contentType string, content []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(content)
	var b bytes.Buffer
	b.WriteString("From: noreply-dmarc-support@google.com\r\n")
	b.WriteString("Subject: Report Domain: example.test\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"sep\"\r\n\r\n")
	b.WriteString("--sep\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString("This is an aggregate report.\r\n")
	b.WriteString("--sep\r\n")
	b.WriteString("Content-Type: " + contentType + "; name=\"" + filename + "\"\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"" + filename + "\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	b.WriteString("--sep--\r\n")
	return b.Bytes()
}

const tlsRptJSON = `{"organization-name":"Google Inc.",` +
	`"date-range":{"start-datetime":"2026-08-19T00:00:00Z","end-datetime":"2026-08-19T23:59:59Z"},` +
	`"report-id":"2026-08-19T00:00:00Z_example.test",` +
	`"policies":[{"policy":{"policy-type":"no-policy-found","policy-domain":"example.test"},` +
	`"summary":{"total-successful-session-count":2,"total-failure-session-count":0}}]}`

// The exact filename Google uses, which is what arrives in the inbox today.
const googleTlsRptFilename = "google.com!example.test!1787097600!1787183999!001.json.gz"

func newReportIngestor(repo *recordingReportRepo) *IngestDeliveredReportsUseCase {
	return NewIngestDeliveredReportsUseCase(NewIngestReportsUseCase(repo))
}

func TestTlsRptReportAddressedToTlsRptIsIngested(t *testing.T) {
	repo := &recordingReportRepo{}
	uc := newReportIngestor(repo)

	payload := messageWithAttachment(googleTlsRptFilename, "application/gzip", gzipped(t, tlsRptJSON))

	outcome, err := uc.Ingest(context.Background(), []string{"tlsrpt@example.test"}, payload)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome.Kind != ReportKindTlsRpt {
		t.Errorf("kind is %q, want tlsrpt", outcome.Kind)
	}
	if len(repo.tlsRpt) != 1 {
		t.Fatalf("want 1 stored report, got %d", len(repo.tlsRpt))
	}
	if repo.tlsRpt[0].Domain != "example.test" {
		t.Errorf("stored domain is %q", repo.tlsRpt[0].Domain)
	}
}

func TestDmarcReportAddressedToDmarcIsIngested(t *testing.T) {
	repo := &recordingReportRepo{}
	uc := newReportIngestor(repo)

	payload := messageWithAttachment(
		"google.com!example.test!1787097600!1787183999.xml.gz",
		"application/gzip", gzipped(t, dmarcAggregateXMLFixture))

	outcome, err := uc.Ingest(context.Background(), []string{"dmarc@example.test"}, payload)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome.Kind != ReportKindDmarc {
		t.Errorf("kind is %q, want dmarc", outcome.Kind)
	}
	if len(repo.dmarc) != 1 {
		t.Fatalf("want 1 stored report, got %d", len(repo.dmarc))
	}
}

// The address decides, not the subject or the filename - both of which the
// sender chooses freely. Ordinary mail must pass through untouched.
func TestOrdinaryMailIsNotTreatedAsAReport(t *testing.T) {
	repo := &recordingReportRepo{}
	uc := newReportIngestor(repo)

	payload := messageWithAttachment(googleTlsRptFilename, "application/gzip", gzipped(t, tlsRptJSON))

	outcome, err := uc.Ingest(context.Background(), []string{"lucas@example.test"}, payload)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome.Kind != ReportKindNone {
		t.Errorf("ordinary mail was classified as %q", outcome.Kind)
	}
	if len(repo.tlsRpt) != 0 || len(repo.dmarc) != 0 {
		t.Error("a report was stored from mail addressed to a person")
	}
}

// A report this server cannot parse is still a report: it must be recognised,
// so it files with the others, and it must never fail the delivery. Returning
// an error here would have the reporter retry the same broken payload forever.
func TestUnparseableReportIsRecognisedButNeverFailsDelivery(t *testing.T) {
	repo := &recordingReportRepo{}
	uc := newReportIngestor(repo)

	payload := messageWithAttachment("broken.json.gz", "application/gzip", []byte("not a report at all"))

	outcome, err := uc.Ingest(context.Background(), []string{"tlsrpt@example.test"}, payload)
	if err != nil {
		t.Fatalf("a malformed report must not fail the delivery: %v", err)
	}
	if outcome.Kind != ReportKindTlsRpt {
		t.Errorf("kind is %q; the address still says what this message is", outcome.Kind)
	}
	if len(repo.tlsRpt) != 0 {
		t.Error("an unparseable payload was stored")
	}
}

// A storage failure is this server's problem, not the reporter's, and must not
// turn into an SMTP error either.
func TestStorageFailureDoesNotFailDelivery(t *testing.T) {
	repo := &recordingReportRepo{err: errors.New("database is down")}
	uc := newReportIngestor(repo)

	payload := messageWithAttachment(googleTlsRptFilename, "application/gzip", gzipped(t, tlsRptJSON))

	if _, err := uc.Ingest(context.Background(), []string{"tlsrpt@example.test"}, payload); err != nil {
		t.Errorf("a storage failure must not fail the delivery: %v", err)
	}
}

// Reporters vary the local part: dmarc-reports@, tlsrpt@, and the aliases this
// server creates. Matching is on the local part, case-insensitively.
func TestReportAddressMatchingIsCaseInsensitive(t *testing.T) {
	for _, address := range []string{"TLSRPT@example.test", "TlsRpt@Example.Test"} {
		if got := reportKindForAddress(address); got != ReportKindTlsRpt {
			t.Errorf("%s classified as %q, want tlsrpt", address, got)
		}
	}
	if got := reportKindForAddress("DMARC@example.test"); got != ReportKindDmarc {
		t.Errorf("DMARC@ classified as %q", got)
	}
}

// A report with no attachment at all - some reporters inline small ones - must
// still not fail, and must still be recognised by its address.
func TestReportWithNoAttachmentDoesNotFail(t *testing.T) {
	repo := &recordingReportRepo{}
	uc := newReportIngestor(repo)

	payload := []byte("From: a@b.test\r\nSubject: hi\r\n\r\nno attachment here\r\n")
	outcome, err := uc.Ingest(context.Background(), []string{"tlsrpt@example.test"}, payload)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if outcome.Kind != ReportKindTlsRpt {
		t.Errorf("kind is %q", outcome.Kind)
	}
}

// A DMARC aggregate report, trimmed to the fields the parser reads.
const dmarcAggregateXMLFixture = `<?xml version="1.0" encoding="UTF-8" ?>
<feedback>
  <report_metadata>
    <org_name>google.com</org_name>
    <report_id>987654321</report_id>
    <date_range><begin>1787097600</begin><end>1787183999</end></date_range>
  </report_metadata>
  <policy_published><domain>example.test</domain><p>quarantine</p></policy_published>
  <record>
    <row>
      <source_ip>203.0.113.10</source_ip>
      <count>1</count>
      <policy_evaluated><disposition>none</disposition><dkim>pass</dkim><spf>pass</spf></policy_evaluated>
    </row>
    <identifiers><header_from>example.test</header_from></identifiers>
  </record>
</feedback>`

// --- the delivery path ---------------------------------------------------

// A report must be ingested and filed out of the way in one delivery, without
// the address ever having to be a real mailbox the user reads.
func TestDeliveredReportIsIngestedAndFiledInReports(t *testing.T) {
	repo := &recordingReportRepo{}
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetReportIngestor(newReportIngestor(repo))

	payload := messageWithAttachment(googleTlsRptFilename, "application/gzip", gzipped(t, tlsRptJSON))
	if err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "smtp-tls-reporting@google.com",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"tlsrpt@example.test"},
		Body:               bytes.NewReader(payload),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(repo.tlsRpt) != 1 {
		t.Fatalf("the report was not ingested: %d stored", len(repo.tlsRpt))
	}
	if len(messages.persisted) != 1 {
		t.Fatalf("want the message filed once, got %d", len(messages.persisted))
	}
	filed := messages.persisted[0]
	if filed.TargetFolderName != ReportsFolderName {
		t.Errorf("the report was filed in %q, want %q", filed.TargetFolderName, ReportsFolderName)
	}
	// It is machine-read mail; raising an unread badge for it would recreate
	// the inbox noise this exists to remove.
	if !filed.AlreadySeen {
		t.Error("the filed report was counted as unread mail")
	}
}

// Ordinary mail must reach the inbox exactly as before.
func TestOrdinaryMailStillReachesTheInbox(t *testing.T) {
	repo := &recordingReportRepo{}
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)
	uc.SetReportIngestor(newReportIngestor(repo))

	if err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "friend@example.test",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"lucas@example.test"},
		Body:               bytes.NewBufferString("Subject: hello\r\n\r\nhi"),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	filed := messages.persisted[0]
	if filed.TargetFolderName == ReportsFolderName {
		t.Error("ordinary mail was filed as a report")
	}
	if filed.AlreadySeen {
		t.Error("ordinary mail was filed as already read")
	}
}

// With no ingestor configured, delivery must behave exactly as it did before
// this existed - reports included.
func TestDeliveryIsUnchangedWithoutAnIngestor(t *testing.T) {
	messages := &recordingMessageRepository{}
	uc := NewProcessInboundEmailUseCase(nil, &capturingBlobStorage{}, messages)

	payload := messageWithAttachment(googleTlsRptFilename, "application/gzip", gzipped(t, tlsRptJSON))
	if err := uc.Handle(context.Background(), ProcessInboundEmailInput{
		Sender:             "smtp-tls-reporting@google.com",
		Recipients:         []port.MailboxRecord{{ID: uuid.New()}},
		RecipientAddresses: []string{"tlsrpt@example.test"},
		Body:               bytes.NewReader(payload),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if messages.persisted[0].TargetFolderName == ReportsFolderName {
		t.Error("a report was routed with no ingestor configured")
	}
}
