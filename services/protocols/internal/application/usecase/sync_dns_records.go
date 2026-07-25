package usecase

import (
	"context"
	"fmt"
	"strings"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

type SyncDnsRecordsInput struct {
	DomainName        string
	MailHost          string
	ServerIPv4        string
	ServerIPv6        string
	RsaDkimPubKey     string
	EdDkimPubKey      string
	TlsaHash          string
	DaneEnabled       bool
	AdminEmailAddress string
}

type SyncDnsRecordsOutput struct {
	ZoneID         string
	CreatedCount   int
	UpdatedCount   int
	UnchangedCount int
}

type SyncDnsRecordsUseCase struct {
	dnsProvider port.DnsProvider
	aliasRepo   port.SystemAliasRepository
}

func NewSyncDnsRecordsUseCase(dnsProvider port.DnsProvider, aliasRepo port.SystemAliasRepository) *SyncDnsRecordsUseCase {
	return &SyncDnsRecordsUseCase{
		dnsProvider: dnsProvider,
		aliasRepo:   aliasRepo,
	}
}

func (uc *SyncDnsRecordsUseCase) Execute(ctx context.Context, input SyncDnsRecordsInput) (*SyncDnsRecordsOutput, error) {
	if input.DomainName == "" {
		return nil, fmt.Errorf("domain name is required")
	}

	desiredSpecs := entity.Build13DnsRecordSpecs(
		input.DomainName,
		input.MailHost,
		input.ServerIPv4,
		input.ServerIPv6,
		input.RsaDkimPubKey,
		input.EdDkimPubKey,
		input.TlsaHash,
		input.DaneEnabled,
	)

	zoneID, err := uc.dnsProvider.GetZoneID(ctx, input.DomainName)
	if err != nil {
		return nil, fmt.Errorf("get zone id for %s: %w", input.DomainName, err)
	}

	existingRecords, err := uc.dnsProvider.ListRecords(ctx, zoneID)
	if err != nil {
		return nil, fmt.Errorf("list dns records for zone %s: %w", zoneID, err)
	}

	existingMap := make(map[string]entity.DnsRecord)
	for _, rec := range existingRecords {
		key := fmt.Sprintf("%s:%s", strings.ToUpper(rec.Type), strings.ToLower(rec.Name))
		existingMap[key] = rec
	}

	output := &SyncDnsRecordsOutput{ZoneID: zoneID}

	for _, spec := range desiredSpecs {
		key := fmt.Sprintf("%s:%s", strings.ToUpper(spec.Type), strings.ToLower(spec.Name))
		existing, found := existingMap[key]

		if !found {
			if err := uc.dnsProvider.CreateRecord(ctx, zoneID, spec); err != nil {
				return nil, fmt.Errorf("create dns record %s %s: %w", spec.Type, spec.Name, err)
			}
			output.CreatedCount++
		} else if !spec.EqualsNormalized(existing) {
			spec.ID = existing.ID
			if err := uc.dnsProvider.UpdateRecord(ctx, zoneID, spec); err != nil {
				return nil, fmt.Errorf("update dns record %s %s: %w", spec.Type, spec.Name, err)
			}
			output.UpdatedCount++
		} else {
			output.UnchangedCount++
		}
	}

	if input.AdminEmailAddress != "" && uc.aliasRepo != nil {
		if err := uc.aliasRepo.EnsureSystemAliases(ctx, input.DomainName, input.AdminEmailAddress); err != nil {
			return nil, fmt.Errorf("ensure system aliases: %w", err)
		}
	}

	return output, nil
}
