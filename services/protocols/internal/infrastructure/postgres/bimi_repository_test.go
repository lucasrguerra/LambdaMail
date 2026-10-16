package postgres

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestBimiRepositoryHoldsALogoAndItsCertificate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	name := "bimi" + uuid.NewString()[:8] + ".test"
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO domains (id, name, punycode_name, is_active) VALUES ($1,$2,$3,true)`, id, name, name); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM domains WHERE id = $1`, id) })

	repo := NewBimiRepository(pool)
	svg := []byte(`<svg version="1.2" baseProfile="tiny-ps" viewBox="0 0 1 1"><title>x</title></svg>`)
	pem := []byte("-----BEGIN CERTIFICATE-----\nQUJD\n-----END CERTIFICATE-----\n")

	// A certificate means something only next to a logo
	saved, err := repo.SaveCertificate(ctx, name, pem)
	if err != nil || saved {
		t.Fatalf("SaveCertificate with no logo = (%v, %v), want (false, nil)", saved, err)
	}

	if _, err := repo.Save(ctx, name, svg, "https://elsewhere.test/vmc.pem"); err != nil {
		t.Fatal(err)
	}
	logo, err := repo.Load(ctx, name)
	if err != nil || logo == nil || len(logo.VmcPEM) != 0 || logo.VmcURL != "https://elsewhere.test/vmc.pem" {
		t.Fatalf("a logo with no certificate: %+v, %v", logo, err)
	}

	if saved, err := repo.SaveCertificate(ctx, name, pem); err != nil || !saved {
		t.Fatalf("SaveCertificate = (%v, %v)", saved, err)
	}
	logo, _ = repo.Load(ctx, name)
	if !bytes.Equal(logo.VmcPEM, pem) {
		t.Errorf("certificate read back = %q", logo.VmcPEM)
	}

	// Replacing the logo keeps the certificate: it is the operator who decides
	// whether it still applies, and the console says when it does not
	if _, err := repo.Save(ctx, name, append(svg, ' '), ""); err != nil {
		t.Fatal(err)
	}
	logo, _ = repo.Load(ctx, name)
	if !bytes.Equal(logo.VmcPEM, pem) {
		t.Error("replacing the logo dropped the certificate")
	}

	if err := repo.DeleteCertificate(ctx, name); err != nil {
		t.Fatal(err)
	}
	logo, _ = repo.Load(ctx, name)
	if logo == nil || len(logo.VmcPEM) != 0 || len(logo.SVG) == 0 {
		t.Errorf("deleting the certificate must leave the logo: %+v", logo)
	}

	// The certificate goes with the logo
	repo.SaveCertificate(ctx, name, pem)
	if err := repo.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
	if logo, _ := repo.Load(ctx, name); logo != nil {
		t.Errorf("the logo survived its own deletion: %+v", logo)
	}
}
