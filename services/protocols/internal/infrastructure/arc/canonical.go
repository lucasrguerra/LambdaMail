// Package arc implements Authenticated Received Chain (RFC 8617).
//
// ARC exists for the case DKIM cannot cover: a mailing list or forwarder that
// legitimately modifies a message breaks its DKIM signature, and the receiving
// domain then sees a DMARC failure for mail that was authentic when it was
// sent. An ARC chain records each intermediary's own authentication verdict
// and seals it, so the final receiver can see that the message authenticated
// correctly at the first hop (PLAN.md section 5).
//
// Nothing in the Go ecosystem provides ARC, so the signature construction is
// implemented here on top of the same relaxed canonicalization DKIM uses
// (RFC 6376 section 3.4.2), which RFC 8617 mandates for both ARC signatures.
package arc

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// Header names defined by RFC 8617 section 4.1.
const (
	HeaderAuthenticationResults = "ARC-Authentication-Results"
	HeaderMessageSignature      = "ARC-Message-Signature"
	HeaderSeal                  = "ARC-Seal"
)

// header is one parsed header field, kept in the order it appeared.
type header struct {
	name  string
	value string
	raw   string
}

// canonicalizeHeaderRelaxed renders a header field the way RFC 6376 section
// 3.4.2 prescribes: lowercase name, whitespace runs folded to a single space,
// no space around the colon, no trailing whitespace.
func canonicalizeHeaderRelaxed(name, value string) string {
	name = strings.ToLower(strings.TrimSpace(name))

	// Unfold: a continuation line is part of the same field value.
	value = strings.ReplaceAll(value, "\r\n", "")
	value = strings.ReplaceAll(value, "\n", "")

	value = strings.Join(strings.Fields(value), " ")
	return name + ":" + value
}

// canonicalizeBodyRelaxed applies RFC 6376 section 3.4.4: trailing whitespace
// removed from each line, internal whitespace runs collapsed, trailing empty
// lines removed, and a single CRLF kept when the body is not empty.
func canonicalizeBodyRelaxed(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(body, "\n")

	for i, line := range lines {
		lines[i] = strings.TrimRight(strings.Join(strings.Fields(line), " "), " \t")
	}

	// Drop trailing empty lines.
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	lines = lines[:end]

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// bodyHash is the "bh=" tag: the base64 SHA-256 of the canonicalized body.
func bodyHash(body string) string {
	sum := sha256.Sum256([]byte(canonicalizeBodyRelaxed(body)))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// splitMessage separates the header block from the body, keeping the headers
// in order and with their continuation lines joined.
func splitMessage(message string) (headers []header, body string) {
	normalized := strings.ReplaceAll(message, "\r\n", "\n")

	headerBlock, body, found := strings.Cut(normalized, "\n\n")
	if !found {
		headerBlock = normalized
		body = ""
	}

	var current *header
	for _, line := range strings.Split(headerBlock, "\n") {
		if line == "" {
			continue
		}
		// A line starting with whitespace continues the previous field.
		if (line[0] == ' ' || line[0] == '\t') && current != nil {
			current.value += " " + strings.TrimSpace(line)
			current.raw += "\r\n" + line
			continue
		}

		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		headers = append(headers, header{
			name:  strings.TrimSpace(name),
			value: strings.TrimSpace(value),
			raw:   line,
		})
		current = &headers[len(headers)-1]
	}

	return headers, body
}

// sign produces the base64 signature over data for either supported key type.
func sign(signer crypto.Signer, data []byte) (string, error) {
	// RFC 8617 section 4.1.2 allows rsa-sha256 and, following RFC 8463,
	// ed25519-sha256. Ed25519 signs the message itself; RSA signs its digest.
	if _, isEd25519 := signer.Public().(ed25519.PublicKey); isEd25519 {
		signature, err := signer.Sign(rand.Reader, data, crypto.Hash(0))
		if err != nil {
			return "", fmt.Errorf("ed25519 sign: %w", err)
		}
		return base64.StdEncoding.EncodeToString(signature), nil
	}

	digest := sha256.Sum256(data)
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("rsa sign: %w", err)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// algorithmFor names the algorithm in the "a=" tag.
func algorithmFor(signer crypto.Signer) string {
	if _, isEd25519 := signer.Public().(ed25519.PublicKey); isEd25519 {
		return "ed25519-sha256"
	}
	return "rsa-sha256"
}
