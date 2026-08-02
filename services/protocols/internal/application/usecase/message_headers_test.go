package usecase

import "testing"

func TestExtractMessageHeaders_ReadsSubjectSenderAndSnippet(t *testing.T) {
	msg := "From: Alice Example <alice@remote.test>\r\n" +
		"To: bob@example.test\r\n" +
		"Subject: Quarterly report\r\n" +
		"Message-ID: <abc123@remote.test>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Here is the report you asked for.\r\nSecond line.\r\n"

	h := ExtractMessageHeaders([]byte(msg))

	if h.Subject != "Quarterly report" {
		t.Errorf("Subject = %q", h.Subject)
	}
	if h.FromDisplayName != "Alice Example" {
		t.Errorf("FromDisplayName = %q", h.FromDisplayName)
	}
	if h.MessageID != "abc123@remote.test" {
		t.Errorf("MessageID = %q, angle brackets should be stripped", h.MessageID)
	}
	if h.Snippet != "Here is the report you asked for. Second line." {
		t.Errorf("Snippet = %q", h.Snippet)
	}
	if h.HasAttachments {
		t.Error("a text/plain message was reported as having attachments")
	}
}

// Real subjects arrive RFC 2047 encoded; showing the raw encoded word in the
// message list is the usual symptom of skipping this.
func TestExtractMessageHeaders_DecodesEncodedWords(t *testing.T) {
	msg := "From: =?UTF-8?B?SsO6bGlv?= <julio@remote.test>\r\n" +
		"Subject: =?UTF-8?Q?Relat=C3=B3rio_mensal?=\r\n" +
		"\r\n" +
		"corpo\r\n"

	h := ExtractMessageHeaders([]byte(msg))
	if h.Subject != "Relat\u00f3rio mensal" {
		t.Errorf("Subject = %q, want the decoded form", h.Subject)
	}
	if h.FromDisplayName != "J\u00falio" {
		t.Errorf("FromDisplayName = %q, want the decoded form", h.FromDisplayName)
	}
}

func TestExtractMessageHeaders_FlagsMultipartMixedAsAttachment(t *testing.T) {
	withAttachment := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/mixed; boundary=xyz\r\n\r\n--xyz\r\n\r\nbody\r\n--xyz--\r\n"
	if !ExtractMessageHeaders([]byte(withAttachment)).HasAttachments {
		t.Error("multipart/mixed should be flagged as carrying attachments")
	}

	// alternative is the same body twice, not an attachment.
	altOnly := "From: a@b.test\r\nSubject: s\r\n" +
		"Content-Type: multipart/alternative; boundary=xyz\r\n\r\n--xyz\r\n\r\nbody\r\n--xyz--\r\n"
	if ExtractMessageHeaders([]byte(altOnly)).HasAttachments {
		t.Error("multipart/alternative should not count as an attachment")
	}
}

// A malformed message must still be delivered - refusing it would lose mail
// over a bad header, which is what spam is full of.
func TestExtractMessageHeaders_SurvivesGarbage(t *testing.T) {
	for _, payload := range []string{"", "not a message at all", "Subject: no body follows"} {
		h := ExtractMessageHeaders([]byte(payload))
		_ = h // no panic, no error: absent metadata is the worst outcome
	}
}

func TestExtractMessageHeaders_TruncatesWithoutBreakingUTF8(t *testing.T) {
	long := ""
	for range 300 {
		long += "\u00e1"
	}
	h := ExtractMessageHeaders([]byte("Subject: s\r\n\r\n" + long + "\r\n"))
	if len(h.Snippet) > snippetLength {
		t.Errorf("snippet is %d bytes, want at most %d", len(h.Snippet), snippetLength)
	}
	for _, r := range h.Snippet {
		if r == '\ufffd' {
			t.Fatal("truncation split a multi-byte rune")
		}
	}
}
