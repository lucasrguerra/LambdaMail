package bimi

import (
	"testing"
	"time"
)

const (
	pubLogo = "https://mail.ebay.com/.well-known/bimi/default.svg"
	pubCert = "https://mail.ebay.com/.well-known/bimi/vmc.pem"
)

func TestPublishNothingWithoutALogo(t *testing.T) {
	if got := Publish(nil, nil, "https://x.test/v.pem", "ebay.com", pubLogo, pubCert, fixtureNow); got != (Publication{}) {
		t.Fatalf("a domain with no logo must publish nothing, got %+v", got)
	}
}

func TestPublishLogoOnly(t *testing.T) {
	got := Publish(read(t, "ebay.svg"), nil, "", "ebay.com", pubLogo, pubCert, fixtureNow)
	if got.LogoURL != pubLogo || got.CertificateURL != "" || got.Withheld != "" {
		t.Fatalf("%+v", got)
	}
}

func TestPublishAnExternalCertificateURL(t *testing.T) {
	got := Publish(read(t, "ebay.svg"), nil, " https://vmc.example.test/x.pem ", "ebay.com", pubLogo, pubCert, fixtureNow)
	if got.CertificateURL != "https://vmc.example.test/x.pem" {
		t.Fatalf("%+v", got)
	}
}

func TestPublishTheHeldCertificateWhileItIsValidForTheLogo(t *testing.T) {
	got := Publish(read(t, "ebay.svg"), read(t, "ebay.pem"), "https://old.test/x.pem", "ebay.com", pubLogo, pubCert, fixtureNow)
	// The held one wins over an external URL: it is the one checked against this logo
	if got.CertificateURL != pubCert || got.Withheld != "" {
		t.Fatalf("%+v", got)
	}
}

// The reason this exists: replacing the logo under a held certificate must not
// leave a record that points receivers at a certificate for another picture.
func TestPublishWithholdsACertificateThatNoLongerMatchesTheLogo(t *testing.T) {
	got := Publish(read(t, "cnn.svg"), read(t, "ebay.pem"), "", "ebay.com", pubLogo, pubCert, fixtureNow)
	if got.LogoURL != pubLogo {
		t.Errorf("the logo must still be published: %+v", got)
	}
	if got.CertificateURL != "" || got.Withheld != "logoMismatch" {
		t.Fatalf("%+v", got)
	}
}

func TestPublishWithholdsAnExpiredCertificate(t *testing.T) {
	got := Publish(read(t, "ebay.svg"), read(t, "ebay.pem"), "", "ebay.com", pubLogo, pubCert, time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC))
	if got.CertificateURL != "" || got.Withheld != "validity" {
		t.Fatalf("%+v", got)
	}
}

func TestPublishFallsBackToTheExternalURLWhenTheHeldCertificateIsWithheld(t *testing.T) {
	got := Publish(read(t, "cnn.svg"), read(t, "ebay.pem"), "https://vmc.example.test/x.pem", "ebay.com", pubLogo, pubCert, fixtureNow)
	if got.CertificateURL != "https://vmc.example.test/x.pem" || got.Withheld == "" {
		t.Fatalf("%+v", got)
	}
}
