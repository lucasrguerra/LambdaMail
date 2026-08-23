package main

import (
	"context"
	"fmt"
	"log"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/application/usecase"
	"lambdamail/protocols/internal/infrastructure/cloudflare"
	"lambdamail/protocols/internal/infrastructure/netdns"
	"lambdamail/protocols/internal/infrastructure/postgres"
	httppresentation "lambdamail/protocols/internal/presentation/http"
)

// domainReconciler publishes one domain's expected records on demand.
//
// The console could tell an operator a record was missing and offer no way to
// create it: the button reached an endpoint that wrote an audit row, and the
// only code able to talk to the DNS provider ran on a timer, for one domain.
type domainReconciler struct {
	run func(ctx context.Context, domain string) (*usecase.SyncDnsRecordsOutput, error)
}

func (d domainReconciler) ReconcileDomain(
	ctx context.Context, domain string,
) (httppresentation.ReconcileResult, error) {
	out, err := d.run(ctx, domain)
	if err != nil {
		return httppresentation.ReconcileResult{}, err
	}

	conflicts := make([]string, 0, len(out.Conflicts))
	for _, c := range out.Conflicts {
		// Named plainly, because the operator has to decide what to do: the
		// existing value may be something they still need.
		conflicts = append(conflicts, fmt.Sprintf("%s %s already holds %q", c.Type, c.Name, c.Existing))
	}

	return httppresentation.ReconcileResult{
		Domain:    domain,
		Created:   out.CreatedCount,
		Updated:   out.UpdatedCount,
		Unchanged: out.UnchangedCount,
		Conflicts: conflicts,
		Errors:    out.Errors,
	}, nil
}

// reconcileDomainOnDemand publishes one domain's records, now.
//
// It builds the same use case the timer uses rather than sharing an instance:
// the console's button and the background sweep must not be able to interleave
// halfway through each other's zone updates.
func reconcileDomainOnDemand(
	ctx context.Context, cfg config, aliasRepo port.SystemAliasRepository,
	dkimRepo *postgres.DkimRepository, domain string,
) (*usecase.SyncDnsRecordsOutput, error) {
	syncUC := usecase.NewSyncDnsRecordsUseCase(cloudflare.NewCloudflareAdapter(cfg.CloudflareToken), aliasRepo)
	syncUC.SetVerifier(netdns.NewPublicVerifier())

	input := usecase.SyncDnsRecordsInput{
		DomainName:      domain,
		MailHost:        cfg.PrimaryMailHost,
		ServerIPv4:      cfg.PublicIPv4,
		ServerIPv6:      cfg.PublicIPv6,
		DaneEnabled:     cfg.OutboundDane,
		RelaySpfInclude: relayConfig(cfg).SpfInclude(),
	}

	// A domain with no DKIM key of its own would publish a selector pointing
	// at nothing, so the keys are provisioned first, exactly as the timer does.
	if dkimRepo != nil {
		keys, err := usecase.NewProvisionDkimKeysUseCase(dkimRepo, generateDkimKey).Execute(ctx, domain)
		if err != nil {
			return nil, fmt.Errorf("provision DKIM keys for %s: %w", domain, err)
		}
		input.RsaDkimPubKey = keys.RsaPublicKey
		input.EdDkimPubKey = keys.Ed25519PublicKey
		if len(keys.Created) > 0 {
			log.Printf("DNS reconcile (on demand): generated DKIM keys for %s: %v", domain, keys.Created)
		}
	}

	out, err := syncUC.Execute(ctx, input)
	if err != nil {
		return nil, err
	}
	log.Printf("DNS reconcile (on demand) for %s: %s (created=%d updated=%d unchanged=%d conflicts=%d)",
		domain, out.Status, out.CreatedCount, out.UpdatedCount, out.UnchangedCount, out.ConflictCount)
	return out, nil
}
