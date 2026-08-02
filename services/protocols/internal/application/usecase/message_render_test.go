package usecase

import "testing"

func TestRenderMessage_ExtractsHeadersAndText(t *testing.T) {
	raw := "From: Alice <alice@remote.test>\r\n" +
		"To: bob@example.test\r\n" +
		"Subject: =?UTF-8?Q?Ol=C3=A1?=\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nCorpo.\r\n"

	got := RenderMessage([]byte(raw), 7)
	if got.UID != 7 || got.Subject != "Ol\u00e1" {
		t.Errorf("UID/Subject = %d/%q", got.UID, got.Subject)
	}
	if got.Text != "Corpo.\r\n" {
		t.Errorf("Text = %q", got.Text)
	}
	if got.Date == "" {
		t.Error("Date was not parsed")
	}
}

// A message with no Date header parses into the zero time without an error;
// rendering that verbatim showed the year 0001 in the reader.
func TestRenderMessage_LeavesDateEmptyWhenHeaderAbsent(t *testing.T) {
	got := RenderMessage([]byte("From: a@b.test\r\nSubject: s\r\n\r\nbody\r\n"), 1)
	if got.Date != "" {
		t.Errorf("Date = %q, want empty so the UI falls back to received_at", got.Date)
	}
}

func TestRenderMessage_PrefersPlainTextAndListsAttachments(t *testing.T) {
	raw := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nplain part\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<p>html part</p>\r\n" +
		"--B\r\nContent-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"report.pdf\"\r\n\r\nPDFDATA\r\n--B--\r\n"

	got := RenderMessage([]byte(raw), 1)
	if got.Text != "plain part" {
		t.Errorf("Text = %q", got.Text)
	}
	if got.HTML != "<p>html part</p>" {
		t.Errorf("HTML = %q", got.HTML)
	}
	if len(got.Attachments) != 1 || got.Attachments[0] != "report.pdf" {
		t.Errorf("Attachments = %v", got.Attachments)
	}
}

// Garbage must still open: an unreadable message beats an inbox that errors.
func TestRenderMessage_FallsBackToRawOnParseFailure(t *testing.T) {
	got := RenderMessage([]byte("this is not a message"), 3)
	if got.Text == "" {
		t.Error("a malformed message produced no body at all")
	}
}
