package bimi

import (
	"strings"
	"time"
)

// Publication is what the domain's BIMI DNS record should say right now.
//
// One decision, taken in one place, because three things have to agree on it:
// the record the reconciler writes to the zone, the one the console expects to
// find there, and the setup check. They used to compute it separately, and the
// reconciler did not compute it at all, so a logo that was stored was never
// published.
type Publication struct {
	// LogoURL is empty when the domain has no logo: then there is no record.
	LogoURL string
	// CertificateURL is the a= tag, empty when none should be published.
	CertificateURL string
	// Withheld says why a certificate that is held here is not being published.
	Withheld string
}

// Publish decides the record for a domain.
//
// A certificate held here is published only while it is valid for the logo
// being served. The certificate vouches for one exact SVG, so the moment the
// logo is replaced it stops applying, and a record that still names it makes
// receivers reject BIMI outright, including the ones that would have shown the
// logo without a certificate. Withholding it keeps those working and leaves the
// reason for the console to show.
func Publish(svg, pem []byte, externalURL, domain, logoURL, hostedCertificateURL string, now time.Time) Publication {
	if len(svg) == 0 {
		return Publication{}
	}
	p := Publication{LogoURL: logoURL, CertificateURL: strings.TrimSpace(externalURL)}

	if len(pem) == 0 {
		return p
	}

	if _, violations := InspectCertificate(pem, domain, svg, now); len(violations) > 0 {
		rules := make([]string, 0, len(violations))
		for _, v := range violations {
			rules = append(rules, v.Rule)
		}
		p.Withheld = strings.Join(rules, ", ")
		return p
	}
	p.CertificateURL = hostedCertificateURL
	return p
}
