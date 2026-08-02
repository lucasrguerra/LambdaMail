package usecase

import (
	"bytes"
	"io"
	"mime"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// snippetLength is how much of the body is kept for the message list. It is a
// preview, not the message: the body itself stays in the blob.
const snippetLength = 200

// MessageHeaders is the subset of a message the webmail list needs, so that
// showing an inbox does not mean reading every blob off disk.
type MessageHeaders struct {
	Subject         string
	FromDisplayName string
	MessageID       string
	Snippet         string
	HasAttachments  bool
}

// wordDecoder tolerates the charsets that turn up in real mail. An unknown
// charset yields the raw bytes rather than an error, because a subject that
// renders imperfectly beats a delivery that fails.
var wordDecoder = mime.WordDecoder{
	CharsetReader: func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	},
}

func decodeHeader(value string) string {
	decoded, err := wordDecoder.DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

// ExtractMessageHeaders parses the fields the message list displays.
//
// Every failure is non-fatal: a message with unparseable headers is still
// delivered, just with less metadata. Refusing it would lose mail over a
// malformed Subject, which is exactly the kind of thing spam is full of.
func ExtractMessageHeaders(payload []byte) MessageHeaders {
	var out MessageHeaders

	msg, err := mail.ReadMessage(bytes.NewReader(payload))
	if err != nil {
		return out
	}

	out.Subject = truncateUTF8(decodeHeader(msg.Header.Get("Subject")), 998)
	out.MessageID = truncateUTF8(strings.Trim(msg.Header.Get("Message-ID"), "<>"), 998)

	if from := msg.Header.Get("From"); from != "" {
		if addr, err := mail.ParseAddress(from); err == nil {
			out.FromDisplayName = truncateUTF8(decodeHeader(addr.Name), 255)
		} else {
			// A From that does not parse still often carries a usable name
			// before the angle bracket.
			if idx := strings.Index(from, "<"); idx > 0 {
				out.FromDisplayName = truncateUTF8(decodeHeader(strings.TrimSpace(strings.Trim(from[:idx], `"`))), 255)
			}
		}
	}

	contentType := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		// mixed and related carry parts that are not the body; alternative is
		// just the same text twice, so it does not count as an attachment.
		out.HasAttachments = mediaType != "multipart/alternative"
		_ = params
	}

	out.Snippet = buildSnippet(msg, mediaType)
	return out
}

// buildSnippet takes the opening of the body as a plain-text preview.
func buildSnippet(msg *mail.Message, mediaType string) string {
	// Only a flat text body is previewed. Walking MIME parts here would mean
	// decoding attachments on the delivery path, which is the hot path.
	if mediaType != "" && !strings.HasPrefix(mediaType, "text/") {
		return ""
	}

	buf := make([]byte, 8*1024)
	n, _ := msg.Body.Read(buf)
	if n <= 0 {
		return ""
	}

	text := string(buf[:n])
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.Join(strings.Fields(text), " ")
	return truncateUTF8(text, snippetLength)
}

// truncateUTF8 cuts to at most max bytes without splitting a rune, so a
// truncated subject cannot become invalid UTF-8 and break the JSON encoding.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
