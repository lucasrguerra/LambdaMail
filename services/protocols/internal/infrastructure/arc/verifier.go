package arc

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// VerifyChain checks an ARC chain against the public keys of the sealing
// domains and returns the chain validation state a downstream hop should
// report in its own "cv=" tag.
//
// publicKeyFor resolves the key for a domain and selector; it is injected so
// the caller decides how lookups happen (DNS in production, a fixed map in a
// test).
func VerifyChain(message []byte, publicKeyFor func(domain, selector string) (crypto.PublicKey, error)) (ChainValidation, error) {
	headers, body := splitMessage(string(message))

	highest := 0
	for _, h := range headers {
		if strings.EqualFold(h.name, HeaderSeal) {
			if i, err := strconv.Atoi(parseTags(h.value)["i"]); err == nil && i > highest {
				highest = i
			}
		}
	}

	if highest == 0 {
		return ChainNone, nil
	}
	if highest > maxChainInstance {
		return ChainFail, fmt.Errorf("arc: chain exceeds the maximum of %d instances", maxChainInstance)
	}

	// RFC 8617 section 5.2: the most recent ARC-Message-Signature must
	// validate, and every ARC-Seal in the chain must validate.
	if err := verifyMessageSignature(headers, body, highest, publicKeyFor); err != nil {
		return ChainFail, err
	}

	for instance := 1; instance <= highest; instance++ {
		if err := verifySeal(headers, instance, publicKeyFor); err != nil {
			return ChainFail, err
		}
	}

	return ChainPass, nil
}

func verifyMessageSignature(headers []header, body string, instance int, publicKeyFor func(string, string) (crypto.PublicKey, error)) error {
	value, ok := findInstanceHeader(headers, HeaderMessageSignature, instance)
	if !ok {
		return fmt.Errorf("arc: no %s for instance %d", HeaderMessageSignature, instance)
	}

	tags := parseTags(value)
	if tags["bh"] != bodyHash(body) {
		return fmt.Errorf("arc: body hash mismatch at instance %d", instance)
	}

	signedNames := strings.Split(tags["h"], ":")
	material := canonicalHeaderBlock(headers, signedNames)
	material += canonicalizeHeaderRelaxed(HeaderMessageSignature, stripSignature(value))

	return verifySignature(tags, []byte(material), publicKeyFor)
}

func verifySeal(headers []header, instance int, publicKeyFor func(string, string) (crypto.PublicKey, error)) error {
	value, ok := findInstanceHeader(headers, HeaderSeal, instance)
	if !ok {
		return fmt.Errorf("arc: no %s for instance %d", HeaderSeal, instance)
	}

	var material strings.Builder
	for i := 1; i < instance; i++ {
		for _, name := range []string{HeaderAuthenticationResults, HeaderMessageSignature, HeaderSeal} {
			if previous, ok := findInstanceHeader(headers, name, i); ok {
				material.WriteString(canonicalizeHeaderRelaxed(name, previous))
			}
		}
	}

	if aar, ok := findInstanceHeader(headers, HeaderAuthenticationResults, instance); ok {
		material.WriteString(canonicalizeHeaderRelaxed(HeaderAuthenticationResults, aar))
	}
	if ams, ok := findInstanceHeader(headers, HeaderMessageSignature, instance); ok {
		material.WriteString(canonicalizeHeaderRelaxed(HeaderMessageSignature, ams))
	}
	material.WriteString(canonicalizeHeaderRelaxed(HeaderSeal, stripSignature(value)))

	return verifySignature(parseTags(value), []byte(material.String()), publicKeyFor)
}

// stripSignature blanks the "b=" tag, which is how the signature over a header
// that contains itself is defined (RFC 6376 section 3.7).
func stripSignature(value string) string {
	index := strings.LastIndex(value, "b=")
	if index < 0 {
		return value
	}
	return value[:index+2]
}

func verifySignature(tags map[string]string, material []byte, publicKeyFor func(string, string) (crypto.PublicKey, error)) error {
	signature, err := base64.StdEncoding.DecodeString(tags["b"])
	if err != nil {
		return fmt.Errorf("arc: signature is not valid base64: %w", err)
	}

	key, err := publicKeyFor(tags["d"], tags["s"])
	if err != nil {
		return fmt.Errorf("arc: cannot resolve key for %s/%s: %w", tags["d"], tags["s"], err)
	}

	switch typed := key.(type) {
	case ed25519.PublicKey:
		if !ed25519.Verify(typed, material, signature) {
			return fmt.Errorf("arc: ed25519 signature does not verify for %s", tags["d"])
		}
		return nil
	case *rsa.PublicKey:
		digest := sha256.Sum256(material)
		if err := rsa.VerifyPKCS1v15(typed, crypto.SHA256, digest[:], signature); err != nil {
			return fmt.Errorf("arc: rsa signature does not verify for %s: %w", tags["d"], err)
		}
		return nil
	default:
		return fmt.Errorf("arc: unsupported key type %T", key)
	}
}
