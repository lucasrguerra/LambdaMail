package bimi

import (
	"bytes"
	"compress/gzip"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"strings"
	"time"
)

// A BIMI certificate is not something this service can make. A Verified Mark
// Certificate (VMC) or Common Mark Certificate (CMC) is issued by a certificate
// authority after it has checked the organisation, and, for a VMC, a registered
// trademark. What the service can do is hold the certificate the authority
// returns, serve it where the DNS record points, and check it against the logo
// before anything is published, because a certificate whose embedded logo is
// not the logo at the "l=" URL makes the receiver ignore BIMI without a word.

var (
	// id-kp-BrandIndicatorforMessageIdentification (RFC 9495 / BIMI draft).
	oidBrandIndicatorEKU = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 31}
	// id-pe-logotype (RFC 3709): where the logo is embedded.
	oidLogotype = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 12}
	// The mark type the issuer records in the subject: "Registered Mark" for a
	// VMC, "Prior Use Mark" for a CMC.
	oidMarkType = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 53087, 1, 13}
)

// MaxCertificateBytes bounds what is accepted. Real chains are about 10 KB.
const MaxCertificateBytes = 64 * 1024

// Kind is what sort of mark certificate this is.
type Kind string

const (
	KindVMC     Kind = "vmc"
	KindCMC     Kind = "cmc"
	KindUnknown Kind = "unknown"
)

// Certificate describes a parsed BIMI certificate chain for the console.
type Certificate struct {
	Subject      string
	Organization string
	Issuer       string
	NotBefore    time.Time
	NotAfter     time.Time
	DNSNames     []string
	MarkType     string
	Kind         Kind
	ChainLength  int
	// MatchesLogo is true when the logo embedded in the certificate is, byte
	// for byte, the logo being published.
	MatchesLogo bool
	Warnings    []string
}

// InspectCertificate parses a PEM chain and reports every reason it would not
// work for domain and logo. A nil Certificate means nothing could be read.
//
// All reasons at once, for the same reason Validate reports all of its own.
func InspectCertificate(pemData []byte, domain string, logo []byte, now time.Time) (*Certificate, []Violation) {
	var out []Violation
	add := func(rule, detail string) { out = append(out, Violation{Rule: rule, Detail: detail}) }

	if len(pemData) == 0 {
		return nil, []Violation{{Rule: "empty", Detail: "the file is empty"}}
	}
	if len(pemData) > MaxCertificateBytes {
		return nil, []Violation{{Rule: "size", Detail: fmt.Sprintf("%d bytes; the limit is %d", len(pemData), MaxCertificateBytes)}}
	}

	var certs []*x509.Certificate
	rest := pemData
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		// Anything but a certificate is refused outright. A private key pasted
		// here by mistake would be served to the whole Internet from a public URL.
		if block.Type != "CERTIFICATE" {
			return nil, []Violation{{Rule: "pemBlock", Detail: fmt.Sprintf("found a %q block; only CERTIFICATE blocks are accepted, and never a private key", block.Type)}}
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, []Violation{{Rule: "certificate", Detail: "a certificate in the file could not be parsed"}}
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, []Violation{{Rule: "pem", Detail: "no PEM certificate found"}}
	}

	leaf := certs[0]
	info := &Certificate{
		Subject:     leaf.Subject.CommonName,
		Issuer:      leaf.Issuer.CommonName,
		NotBefore:   leaf.NotBefore,
		NotAfter:    leaf.NotAfter,
		DNSNames:    leaf.DNSNames,
		ChainLength: len(certs),
		Kind:        KindUnknown,
	}
	if len(leaf.Subject.Organization) > 0 {
		info.Organization = leaf.Subject.Organization[0]
	}
	info.MarkType = markType(leaf)
	switch {
	case strings.Contains(strings.ToLower(info.MarkType), "registered"):
		info.Kind = KindVMC
	case strings.Contains(strings.ToLower(info.MarkType), "prior use"):
		info.Kind = KindCMC
	}

	if !hasBrandIndicatorEKU(leaf) {
		add("extendedKeyUsage", "the first certificate is not a BIMI certificate (no Brand Indicator for Message Identification usage); the leaf certificate must come first")
	}

	switch {
	case now.Before(leaf.NotBefore):
		add("validity", fmt.Sprintf("not valid until %s", leaf.NotBefore.UTC().Format("2006-01-02")))
	case now.After(leaf.NotAfter):
		add("validity", fmt.Sprintf("expired on %s", leaf.NotAfter.UTC().Format("2006-01-02")))
	case leaf.NotAfter.Sub(now) < 30*24*time.Hour:
		info.Warnings = append(info.Warnings, fmt.Sprintf("expires on %s: ask the authority for a renewal", leaf.NotAfter.UTC().Format("2006-01-02")))
	}

	if !coversDomain(leaf, domain) {
		add("domain", fmt.Sprintf("the certificate does not cover %s (it covers %s)", domain, strings.Join(leaf.DNSNames, ", ")))
	}

	embedded, err := embeddedLogo(leaf)
	switch {
	case err != nil:
		add("logotype", err.Error())
	case !bytes.Equal(embedded, logo):
		add("logoMismatch", "the logo inside the certificate is not the logo published for this domain; upload exactly the SVG the certificate was issued for")
	default:
		info.MatchesLogo = true
	}

	if len(certs) == 1 {
		info.Warnings = append(info.Warnings, "the file holds only the leaf certificate; Gmail and Apple Mail expect the intermediate certificates after it, as the authority delivers them")
	} else {
		for i := 0; i+1 < len(certs); i++ {
			if err := certs[i].CheckSignatureFrom(certs[i+1]); err != nil {
				info.Warnings = append(info.Warnings, fmt.Sprintf("certificate %d was not signed by certificate %d: the chain is out of order or incomplete", i+1, i+2))
				break
			}
		}
	}

	return info, out
}

func hasBrandIndicatorEKU(cert *x509.Certificate) bool {
	for _, usage := range cert.UnknownExtKeyUsage {
		if usage.Equal(oidBrandIndicatorEKU) {
			return true
		}
	}
	return false
}

// markType reads the issuer's mark-type attribute from the subject.
func markType(cert *x509.Certificate) string {
	for _, name := range cert.Subject.Names {
		if name.Type.Equal(oidMarkType) {
			if value, ok := name.Value.(string); ok {
				return value
			}
		}
	}
	return ""
}

// coversDomain applies the rule receivers use: the certificate must name the
// sender's domain, or the organisational domain above it ("ebay.com" covers
// "reply.ebay.com"), exactly or through a wildcard.
func coversDomain(cert *x509.Certificate, domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	for _, name := range cert.DNSNames {
		name = strings.ToLower(name)
		if name == domain || strings.HasSuffix(domain, "."+strings.TrimPrefix(name, "*.")) {
			return true
		}
	}
	return false
}

// embeddedLogo returns the SVG carried in the certificate's logotype extension.
//
// The extension is a nest of ASN.1 structures, but the logo inside is a single
// IA5String holding a data URI, so it is found by that, not by walking the
// structure: "data:image/svg+xml;base64," followed by a gzipped SVG. The type
// says svg+xml and the payload is gzip, which is what issuers actually produce.
func embeddedLogo(cert *x509.Certificate) ([]byte, error) {
	var ext []byte
	for _, e := range cert.Extensions {
		if e.Id.Equal(oidLogotype) {
			ext = e.Value
			break
		}
	}
	if ext == nil {
		return nil, fmt.Errorf("the certificate has no embedded logo (logotype extension)")
	}

	marker := []byte("data:image/svg+xml")
	at := bytes.Index(ext, marker)
	if at < 0 {
		return nil, fmt.Errorf("the embedded logo is not an SVG data URI")
	}
	uri, err := ia5StringAt(ext, at)
	if err != nil {
		return nil, err
	}
	_, payload, found := strings.Cut(uri, ",")
	if !found {
		return nil, fmt.Errorf("the embedded logo is malformed")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("the embedded logo is not valid base64")
	}
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("the embedded logo is not valid gzip")
		}
		// A decompression bomb inside a certificate is still a bomb
		return io.ReadAll(io.LimitReader(reader, 4*MaxLogoBytes))
	}
	return raw, nil
}

// ia5StringAt returns the ASN.1 IA5String whose content begins at offset, by
// reading the tag and length just before it.
func ia5StringAt(der []byte, offset int) (string, error) {
	for _, back := range []int{2, 3, 4} {
		start := offset - back
		if start < 0 || der[start] != 0x16 {
			continue
		}
		var length, header int
		switch lenByte := der[start+1]; {
		case back == 2 && lenByte < 0x80:
			length, header = int(lenByte), 2
		case back == 3 && lenByte == 0x81:
			length, header = int(der[start+2]), 3
		case back == 4 && lenByte == 0x82:
			length, header = int(der[start+2])<<8|int(der[start+3]), 4
		default:
			continue
		}
		if header == back && offset+length <= len(der) {
			return string(der[offset : offset+length]), nil
		}
	}
	return "", fmt.Errorf("the embedded logo could not be located in the certificate")
}
