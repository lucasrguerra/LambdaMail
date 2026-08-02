package usecase

import (
	"bytes"
	"io"
	"strings"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

// maxRenderedPartBytes bounds how much of one part is returned to the browser.
// A message can legitimately be tens of megabytes; the reader pane cannot use
// that, and holding it in memory per request is how a few open tabs turn into
// an out-of-memory kill.
const maxRenderedPartBytes = 2 << 20 // 2 MiB

// RenderedMessage is a message prepared for display: headers the reader shows
// and the two body flavours, already decoded from their transfer encoding.
type RenderedMessage struct {
	UID         uint32   `json:"uid"`
	Subject     string   `json:"subject"`
	From        string   `json:"from"`
	To          []string `json:"to"`
	Cc          []string `json:"cc"`
	Date        string   `json:"date"`
	Text        string   `json:"text"`
	HTML        string   `json:"html"`
	Attachments []string `json:"attachments"`
}

// RenderMessage turns raw RFC 5322 bytes into what the reader pane needs.
//
// Parsing happens here rather than in the browser because this service already
// depends on a MIME library, and a second implementation in TypeScript would
// be a second set of edge cases to get wrong. A message that cannot be parsed
// still yields its raw text rather than an error - unreadable formatting beats
// an inbox that refuses to open a message.
func RenderMessage(raw []byte, uid uint32) RenderedMessage {
	out := RenderedMessage{UID: uid, To: []string{}, Cc: []string{}, Attachments: []string{}}

	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		out.Text = string(raw)
		return out
	}

	header := mail.Header{Header: entity.Header}
	out.Subject, _ = header.Subject()
	if addrs, err := header.AddressList("From"); err == nil && len(addrs) > 0 {
		out.From = addrs[0].String()
	}
	for _, field := range []struct {
		name string
		dst  *[]string
	}{{"To", &out.To}, {"Cc", &out.Cc}} {
		if addrs, err := header.AddressList(field.name); err == nil {
			for _, a := range addrs {
				*field.dst = append(*field.dst, a.String())
			}
		}
	}
	// A missing Date header parses without error into the zero time, which
	// would render as the year 0001 in the reader. Left empty, the UI falls
	// back to the received timestamp it already has.
	if date, err := header.Date(); err == nil && !date.IsZero() {
		out.Date = date.UTC().Format("2006-01-02T15:04:05Z")
	}

	collectParts(entity, &out)
	return out
}

// collectParts walks the MIME tree, keeping the first text and HTML bodies and
// naming everything else as an attachment.
func collectParts(entity *message.Entity, out *RenderedMessage) {
	if multipart := entity.MultipartReader(); multipart != nil {
		for {
			part, err := multipart.NextPart()
			if err != nil {
				return
			}
			collectParts(part, out)
		}
	}

	mediaType, _, err := entity.Header.ContentType()
	if err != nil {
		mediaType = "text/plain"
	}

	if disposition, params, err := entity.Header.ContentDisposition(); err == nil && disposition == "attachment" {
		name := params["filename"]
		if name == "" {
			name = "attachment"
		}
		out.Attachments = append(out.Attachments, name)
		return
	}

	body, err := io.ReadAll(io.LimitReader(entity.Body, maxRenderedPartBytes))
	if err != nil {
		return
	}

	switch {
	case strings.EqualFold(mediaType, "text/plain") && out.Text == "":
		out.Text = string(body)
	case strings.EqualFold(mediaType, "text/html") && out.HTML == "":
		out.HTML = string(body)
	}
}

// ExtractAttachment walks the MIME tree and returns bytes and content-type for a named attachment.
func ExtractAttachment(entity *message.Entity, targetName string) ([]byte, string) {
	if multipart := entity.MultipartReader(); multipart != nil {
		for {
			part, err := multipart.NextPart()
			if err != nil {
				break
			}
			if data, ctype := ExtractAttachment(part, targetName); data != nil {
				return data, ctype
			}
		}
	}

	mediaType, _, _ := entity.Header.ContentType()
	disposition, params, err := entity.Header.ContentDisposition()
	name := params["filename"]
	if name == "" {
		_, paramsCT, _ := entity.Header.ContentType()
		name = paramsCT["name"]
	}

	if (err == nil && disposition == "attachment" && (targetName == "" || strings.EqualFold(name, targetName))) ||
		(targetName != "" && strings.EqualFold(name, targetName)) {
		body, err := io.ReadAll(entity.Body)
		if err == nil {
			return body, mediaType
		}
	}
	return nil, ""
}

