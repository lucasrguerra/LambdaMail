package usecase

import (
	"bytes"
	"testing"

	"github.com/emersion/go-message"
)

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

// An inline image is referenced from the HTML body as cid:<Content-ID>. Neither
// the part nor its identifier used to leave this function, so every message
// written by a normal mail client rendered with broken image placeholders and
// no way for the reader to resolve them.
func TestRenderMessage_ReturnsInlineImagesKeyedByContentID(t *testing.T) {
	// "iVBORw0KGgo=" is the leading bytes of a PNG, base64 encoded.
	raw := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<p><img src=\"cid:logo@x\"></p>\r\n" +
		"--B\r\nContent-Type: image/png\r\n" +
		"Content-ID: <logo@x>\r\n" +
		"Content-Disposition: inline; filename=\"logo.png\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\niVBORw0KGgo=\r\n--B--\r\n"

	got := RenderMessage([]byte(raw), 1)

	// Keyed without the angle brackets, which is how the HTML spells it.
	src, ok := got.InlineImages["logo@x"]
	if !ok {
		t.Fatalf("InlineImages = %v, want an entry for logo@x", got.InlineImages)
	}
	if src != "data:image/png;base64,iVBORw0KGgo=" {
		t.Errorf("InlineImages[logo@x] = %q", src)
	}
}

// An inline image already shown in the body is not a file the reader has to
// download; listing it as an attachment puts a chip under every signature.
func TestRenderMessage_DoesNotListInlineImagesAsAttachments(t *testing.T) {
	raw := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:sig\">\r\n" +
		"--B\r\nContent-Type: image/gif\r\n" +
		"Content-ID: <sig>\r\nContent-Disposition: inline; filename=\"sig.gif\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\nR0lGOD==\r\n--B--\r\n"

	if got := RenderMessage([]byte(raw), 1); len(got.Attachments) != 0 {
		t.Errorf("Attachments = %v, want none", got.Attachments)
	}
}

// A file sent inline that is not an image - a PDF a client attached without a
// disposition of "attachment" - is still something the reader must be able to
// download, so it belongs in the attachment list.
func TestRenderMessage_ListsNonImageInlinePartsAsAttachments(t *testing.T) {
	raw := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"report.pdf\"\r\n" +
		"Content-Disposition: inline; filename=\"report.pdf\"\r\n\r\nPDFDATA\r\n--B--\r\n"

	got := RenderMessage([]byte(raw), 1)
	if len(got.Attachments) != 1 || got.Attachments[0] != "report.pdf" {
		t.Errorf("Attachments = %v, want [report.pdf]", got.Attachments)
	}
}

// An image big enough to matter is not worth inlining into a JSON response
// that the browser holds in memory; the reader shows the alt text instead.
func TestRenderMessage_SkipsOversizedInlineImages(t *testing.T) {
	huge := make([]byte, maxInlineImageBytes+1024)
	for i := range huge {
		huge[i] = 'A'
	}
	raw := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:big\">\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-ID: <big>\r\n\r\n" + string(huge) + "\r\n--B--\r\n"

	if got := RenderMessage([]byte(raw), 1); len(got.InlineImages) != 0 {
		t.Errorf("InlineImages has %d entries, want none", len(got.InlineImages))
	}
}

// image/svg+xml is a document: it carries script and is the one image type a
// data: URI must never smuggle onto the reader pane.
func TestRenderMessage_RefusesToInlineSvg(t *testing.T) {
	raw := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/related; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/html\r\n\r\n<img src=\"cid:x\">\r\n" +
		"--B\r\nContent-Type: image/svg+xml\r\nContent-ID: <x>\r\n\r\n<svg onload=\"alert(1)\"/>\r\n--B--\r\n"

	if got := RenderMessage([]byte(raw), 1); len(got.InlineImages) != 0 {
		t.Errorf("InlineImages = %v, want svg refused", got.InlineImages)
	}
}

// The chip the reader draws for an inline PDF links at the download endpoint,
// which resolves the name through here. Matching only on a disposition of
// "attachment" made every one of those chips 404.
func TestExtractAttachment_FindsInlineNamedParts(t *testing.T) {
	raw := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--B\r\nContent-Type: application/pdf\r\n" +
		"Content-Disposition: inline; filename=\"report.pdf\"\r\n\r\nPDFDATA\r\n--B--\r\n"

	entity, err := message.Read(bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, ctype := ExtractAttachment(entity, "report.pdf")
	if string(data) != "PDFDATA" || ctype != "application/pdf" {
		t.Errorf("ExtractAttachment = %q/%q", data, ctype)
	}
}
