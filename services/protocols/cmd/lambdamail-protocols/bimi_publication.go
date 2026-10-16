package main

import (
	"context"
	"log"
	"time"

	"lambdamail/protocols/internal/domain/bimi"
	"lambdamail/protocols/internal/infrastructure/postgres"
	httppresentation "lambdamail/protocols/internal/presentation/http"
)

// bimiPublication is what a domain's BIMI record should say, for the sync, the
// background sweep and the console alike. Read each time, so a logo saved a
// moment ago is what gets published and a removed one stops being published.
func bimiPublication(ctx context.Context, repo *postgres.BimiRepository, cfg config, domain string) bimi.Publication {
	if repo == nil {
		return bimi.Publication{}
	}
	logo, err := repo.Load(ctx, domain)
	if err != nil {
		// Unknown is not "no logo": returning an empty publication here would
		// have the sync delete the record over a database hiccup.
		log.Printf("BIMI: could not read the logo for %s, leaving its record alone: %v", domain, err)
		return bimi.Publication{LogoURL: httppresentation.BimiLogoURL(cfg.PrimaryMailHost, domain)}
	}
	if logo == nil {
		return bimi.Publication{}
	}
	return bimi.Publish(
		logo.SVG, logo.VmcPEM, logo.VmcURL, domain,
		httppresentation.BimiLogoURL(cfg.PrimaryMailHost, domain),
		httppresentation.BimiVmcURL(cfg.PrimaryMailHost, domain),
		time.Now(),
	)
}
