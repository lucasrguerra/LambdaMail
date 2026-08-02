package httppresentation

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildMessage returns a message carrying one attachment with the given name
// and content type. Both are chosen by whoever sent the mail, which is the
// whole point: neither can be trusted.
func buildMessage(filename, contentType string) []byte {
	return []byte("From: sender@remote.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--B\r\nContent-Type: " + contentType + "\r\n" +
		"Content-Disposition: attachment; filename=\"" + filename + "\"\r\n\r\n" +
		"<script>alert(1)</script>\r\n--B--\r\n")
}

func download(t *testing.T, raw []byte, requested string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	(&mailAPI{}).handleAttachmentDownload(rec, httptest.NewRequest(http.MethodGet, "/x", nil), raw, requested)
	return rec.Result()
}

// Serving an attachment's own text/html back on the webmail's origin is stored
// XSS: the sender picks the type, and anyone can send this mailbox a message.
func TestAttachmentDownload_DowngradesRenderableContentTypes(t *testing.T) {
	for _, declared := range []string{"text/html", "image/svg+xml", "application/xhtml+xml", "text/javascript"} {
		res := download(t, buildMessage("payload", declared), "payload")
		if got := res.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("%s was served as %q, want application/octet-stream", declared, got)
		}
	}
}

func TestAttachmentDownload_KeepsHarmlessContentTypes(t *testing.T) {
	res := download(t, buildMessage("photo.png", "image/png"), "photo.png")
	if got := res.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
}

// A quote in the filename used to break out of the Content-Disposition
// parameter, which is reachable by any sender.
func TestAttachmentDownload_NeutralisesHostileFilenames(t *testing.T) {
	for _, name := range []string{`a"; x="y`, "a\r\nX-Injected: yes", "../../etc/passwd", "a\x00b"} {
		res := download(t, buildMessage(name, "application/pdf"), name)
		disposition := res.Header.Get("Content-Disposition")

		if strings.Contains(disposition, "X-Injected") {
			t.Errorf("filename %q injected a header: %q", name, disposition)
		}
		if strings.Count(disposition, `"`) > 2 {
			t.Errorf("filename %q broke out of the quoted parameter: %q", name, disposition)
		}
		if strings.Contains(disposition, "/") {
			t.Errorf("filename %q kept a path separator: %q", name, disposition)
		}
	}
}

func TestAttachmentDownload_SetsSniffingAndSandboxGuards(t *testing.T) {
	res := download(t, buildMessage("doc.pdf", "application/pdf"), "doc.pdf")
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options: nosniff is missing, so a browser may sniff the bytes as HTML")
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Error("the response is not sandboxed by CSP")
	}
}

func TestAttachmentDownload_MissingAttachmentIs404(t *testing.T) {
	res := download(t, buildMessage("real.pdf", "application/pdf"), "nonexistent.pdf")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
}
