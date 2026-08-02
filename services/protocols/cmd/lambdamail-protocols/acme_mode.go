package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
	"lambdamail/protocols/internal/infrastructure/acme"
	"lambdamail/protocols/internal/infrastructure/cloudflare"
	"lambdamail/protocols/internal/infrastructure/postgres"
	"lambdamail/protocols/internal/infrastructure/vault"
)

// startAcmeManager brings up mode B of PLAN.md section 8.3.
func startAcmeManager(ctx context.Context, cfg config, pool *pgxpool.Pool, secretVault *vault.SecretVault) (port.CertProvider, error) {
	directoryURL := cfg.AcmeDirectoryURL
	if directoryURL == "" {
		directoryURL = acme.LetsEncryptProduction
	}

	store := postgres.NewAcmeRepository(pool, secretVault, directoryURL)
	issuer := acme.NewIssuer(store, cfg.AcmeEmail, directoryURL, cfg.CloudflareToken)

	publisher := &tlsaPublisher{
		adapter: cloudflare.NewCloudflareAdapter(cfg.CloudflareToken),
		domain:  cfg.domain(),
	}

	// The hosts that need a certificate are the mail host itself plus the two
	// service names of PLAN.md section 7.3, which have to present a valid
	// certificate for MTA-STS and autoconfig to work at all.
	extraHosts := []string{
		"mta-sts." + cfg.domain(),
		"autoconfig." + cfg.domain(),
	}

	manager := acme.NewManager(issuer, store, store, publisher, cfg.PrimaryMailHost, extraHosts, cfg.OutboundDane)

	// Reconciling every six hours is frequent enough for a 90 day certificate
	// and for the propagation waits the rollover schedules.
	if err := manager.Start(ctx, 6*time.Hour); err != nil {
		return nil, err
	}

	log.Printf("lambdamail-protocols managing its own certificate for %s (DANE: %v)", cfg.PrimaryMailHost, cfg.OutboundDane)
	return manager, nil
}

// tlsaPublisher writes the DANE associations the rollover decides on.
//
// It replaces the full set every time rather than computing a delta: the
// rollover always hands over the complete desired state, and a delta would
// risk removing an association that is still needed.
type tlsaPublisher struct {
	adapter *cloudflare.CloudflareAdapter
	domain  string
}

func (p *tlsaPublisher) PublishTlsa(ctx context.Context, mailHost string, digests []string) error {
	if len(digests) == 0 {
		return nil
	}

	zoneID, err := p.adapter.GetZoneID(ctx, p.domain)
	if err != nil {
		return fmt.Errorf("resolve zone for TLSA publication: %w", err)
	}

	existing, err := p.adapter.ListRecords(ctx, zoneID)
	if err != nil {
		return fmt.Errorf("list records for TLSA publication: %w", err)
	}

	name := fmt.Sprintf("_25._tcp.%s", mailHost)
	desired := map[string]bool{}
	for _, digest := range digests {
		desired[fmt.Sprintf("3 1 1 %s", digest)] = true
	}

	// Create the ones that are missing before removing anything, so there is
	// never a moment with fewer associations published than required.
	published := map[string]string{}
	for _, record := range existing {
		if record.Type == "TLSA" && equalFoldTrim(record.Name, name) {
			published[record.Value] = record.ID
		}
	}

	for value := range desired {
		if _, alreadyThere := published[value]; alreadyThere {
			continue
		}
		record := entityTlsaRecord(name, value)
		if err := p.adapter.CreateRecord(ctx, zoneID, record); err != nil {
			return fmt.Errorf("publish TLSA %s: %w", value, err)
		}
		log.Printf("acme: published TLSA association %s", value)
	}

	for value, id := range published {
		if desired[value] {
			continue
		}
		if err := p.adapter.DeleteRecord(ctx, zoneID, id); err != nil {
			// A stale association is harmless to delivery - it only means one
			// extra candidate a validator may match - so this is logged
			// rather than treated as a failure.
			log.Printf("acme: could not remove the retired TLSA association %s: %v", value, err)
			continue
		}
		log.Printf("acme: retired TLSA association %s", value)
	}

	return nil
}

func entityTlsaRecord(name, value string) entity.DnsRecord {
	return entity.DnsRecord{
		Type:    "TLSA",
		Name:    name,
		Value:   value,
		TTL:     1,
		Comment: "LambdaMail DANE TLSA Record",
	}
}

func equalFoldTrim(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}
