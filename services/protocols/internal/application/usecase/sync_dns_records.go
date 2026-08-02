package usecase

import (
	"context"
	"fmt"
	"strings"

	"lambdamail/protocols/internal/application/port"
	"lambdamail/protocols/internal/domain/entity"
)

// SyncStatus is the outcome reported to the admin console (PLAN.md section 7.5).
type SyncStatus string

const (
	SyncStatusVerified SyncStatus = "VERIFIED"
	SyncStatusPartial  SyncStatus = "PARTIAL"
	SyncStatusDrift    SyncStatus = "DRIFT"
)

// ownedRecordCommentPrefix marks the records this reconciler manages. Anything
// in the zone without it that would collide with a desired record belongs to
// somebody else and is reported instead of overwritten.
const ownedRecordCommentPrefix = "LambdaMail"

type SyncDnsRecordsInput struct {
	DomainName    string
	MailHost      string
	ServerIPv4    string
	ServerIPv6    string
	RsaDkimPubKey string
	EdDkimPubKey  string
	// TlsaHash publishes a single DANE association. TlsaHashes supersedes it
	// and is what a rollover uses, since it can carry the current and the
	// next key at once.
	TlsaHash          string
	TlsaHashes        []string
	DaneEnabled       bool
	RelaySpfInclude   string
	AdminEmailAddress string
}

// SyncConflict describes a record that could not be reconciled because a
// foreign record already occupies its name.
type SyncConflict struct {
	Type     string
	Name     string
	Existing string
	Desired  string
}

func (c SyncConflict) String() string {
	return fmt.Sprintf("%s %s: existing %q blocks desired %q", c.Type, c.Name, c.Existing, c.Desired)
}

type SyncDnsRecordsOutput struct {
	ZoneID         string
	Status         SyncStatus
	CreatedCount   int
	UpdatedCount   int
	UnchangedCount int
	ConflictCount  int
	Conflicts      []SyncConflict
	Errors         []string
	// Unverified lists records that were written but could not be read back
	// through the public resolvers.
	Unverified []string
}

type SyncDnsRecordsUseCase struct {
	dnsProvider port.DnsProvider
	aliasRepo   port.SystemAliasRepository
	verifier    port.DnsVerifier
}

func NewSyncDnsRecordsUseCase(dnsProvider port.DnsProvider, aliasRepo port.SystemAliasRepository) *SyncDnsRecordsUseCase {
	return &SyncDnsRecordsUseCase{
		dnsProvider: dnsProvider,
		aliasRepo:   aliasRepo,
	}
}

// SetVerifier enables the independent check of PLAN.md section 7.5: after
// writing, the records are read back through public resolvers. The provider
// accepting a write only proves the API accepted it, not that the record
// resolves for anyone else.
func (uc *SyncDnsRecordsUseCase) SetVerifier(verifier port.DnsVerifier) {
	uc.verifier = verifier
}

func (uc *SyncDnsRecordsUseCase) Execute(ctx context.Context, input SyncDnsRecordsInput) (*SyncDnsRecordsOutput, error) {
	if input.DomainName == "" {
		return nil, fmt.Errorf("domain name is required")
	}

	tlsaHashes := input.TlsaHashes
	if len(tlsaHashes) == 0 && input.TlsaHash != "" {
		tlsaHashes = []string{input.TlsaHash}
	}

	desiredSpecs := entity.BuildDnsRecordSpecs(entity.DnsRecordSpec{
		DomainName:      input.DomainName,
		MailHost:        input.MailHost,
		ServerIPv4:      input.ServerIPv4,
		ServerIPv6:      input.ServerIPv6,
		RsaDkimPubKey:   input.RsaDkimPubKey,
		EdDkimPubKey:    input.EdDkimPubKey,
		TlsaHashes:      tlsaHashes,
		DaneEnabled:     input.DaneEnabled,
		RelaySpfInclude: input.RelaySpfInclude,
	})

	zoneID, err := uc.dnsProvider.GetZoneID(ctx, input.DomainName)
	if err != nil {
		return nil, fmt.Errorf("get zone id for %s: %w", input.DomainName, err)
	}

	existingRecords, err := uc.dnsProvider.ListRecords(ctx, zoneID)
	if err != nil {
		return nil, fmt.Errorf("list dns records for zone %s: %w", zoneID, err)
	}

	// A single type+name can legitimately hold several records (multiple TXT at
	// the zone apex is the common case), so the index maps to every candidate.
	existingByKey := make(map[string][]entity.DnsRecord)
	for _, rec := range existingRecords {
		key := recordKey(rec.Type, rec.Name)
		existingByKey[key] = append(existingByKey[key], rec)
	}

	output := &SyncDnsRecordsOutput{ZoneID: zoneID}

	for _, spec := range desiredSpecs {
		candidates := existingByKey[recordKey(spec.Type, spec.Name)]
		match, foreign := classifyCandidates(spec, candidates)

		switch {
		case match != nil:
			if spec.EqualsNormalized(*match) {
				output.UnchangedCount++
				continue
			}
			spec.ID = match.ID
			if err := uc.dnsProvider.UpdateRecord(ctx, zoneID, spec); err != nil {
				output.Errors = append(output.Errors, fmt.Sprintf("update %s %s: %v", spec.Type, spec.Name, err))
				continue
			}
			output.UpdatedCount++

		case foreign != nil:
			output.ConflictCount++
			output.Conflicts = append(output.Conflicts, SyncConflict{
				Type:     spec.Type,
				Name:     spec.Name,
				Existing: foreign.Value,
				Desired:  spec.Value,
			})

		default:
			if err := uc.dnsProvider.CreateRecord(ctx, zoneID, spec); err != nil {
				output.Errors = append(output.Errors, fmt.Sprintf("create %s %s: %v", spec.Type, spec.Name, err))
				continue
			}
			output.CreatedCount++
		}
	}

	if input.AdminEmailAddress != "" && uc.aliasRepo != nil {
		if err := uc.aliasRepo.EnsureSystemAliases(ctx, input.DomainName, input.AdminEmailAddress); err != nil {
			return nil, fmt.Errorf("ensure system aliases: %w", err)
		}
	}

	output.Unverified = uc.verifyPublished(ctx, desiredSpecs)

	output.Status = SyncStatusVerified
	switch {
	case output.ConflictCount > 0 || len(output.Errors) > 0:
		output.Status = SyncStatusPartial
	case len(output.Unverified) > 0:
		// The records were written but the world cannot see them yet. That is
		// drift, not success: reporting VERIFIED here is what would let a
		// broken zone look healthy.
		output.Status = SyncStatusDrift
	}

	return output, nil
}

// verifyPublished re-reads every desired record through public resolvers and
// returns the ones that did not check out.
func (uc *SyncDnsRecordsUseCase) verifyPublished(ctx context.Context, specs []entity.DnsRecord) []string {
	if uc.verifier == nil {
		return nil
	}

	var unverified []string
	for _, spec := range specs {
		visible, detail := uc.verifier.VerifyRecord(ctx, spec)
		if visible {
			continue
		}
		entry := fmt.Sprintf("%s %s", spec.Type, spec.Name)
		if detail != "" {
			entry += ": " + detail
		}
		unverified = append(unverified, entry)
	}
	return unverified
}

func recordKey(recType, name string) string {
	return strings.ToUpper(recType) + ":" + strings.ToLower(strings.TrimSuffix(name, "."))
}

// classifyCandidates picks, among the records already published under a
// desired record's type and name, the one this reconciler owns. It returns
// either that match, or - when no record is ours and the name admits only a
// single value - the foreign record blocking it.
func classifyCandidates(spec entity.DnsRecord, candidates []entity.DnsRecord) (match *entity.DnsRecord, blocking *entity.DnsRecord) {
	for i := range candidates {
		if isOurRecord(spec, candidates[i]) {
			return &candidates[i], nil
		}
	}
	if len(candidates) > 0 && !allowsCoexistence(spec.Type) {
		return nil, &candidates[0]
	}
	return nil, nil
}

// isOurRecord decides ownership. The record comment is the primary marker, but
// it is not preserved by every provider or migration path, so TXT records fall
// back to their protocol tag: only one "v=spf1" record may exist per name, so a
// record carrying our tag is unambiguously the one we manage.
func isOurRecord(spec, existing entity.DnsRecord) bool {
	if strings.HasPrefix(existing.Comment, ownedRecordCommentPrefix) {
		return true
	}
	if strings.EqualFold(spec.Type, "TXT") {
		tag := txtTag(spec.Value)
		return tag != "" && strings.EqualFold(txtTag(existing.Value), tag)
	}
	// For single-valued names an identical value means the record is already
	// what we want, whoever created it.
	return strings.TrimSpace(spec.Value) == strings.TrimSpace(existing.Value)
}

// txtTag extracts the leading "v=<protocol>" tag that identifies which mail
// protocol a TXT record belongs to (RFC 7208 SPF, RFC 6376 DKIM, RFC 7489
// DMARC, RFC 8461 MTA-STS, RFC 8460 TLS-RPT).
func txtTag(value string) string {
	trimmed := strings.TrimSpace(strings.Trim(value, `"`))
	if !strings.HasPrefix(strings.ToLower(trimmed), "v=") {
		return ""
	}
	rest := trimmed[2:]
	if idx := strings.IndexAny(rest, "; \t"); idx >= 0 {
		rest = rest[:idx]
	}
	return strings.ToLower(rest)
}

// allowsCoexistence reports whether several records may share a type and name.
// TXT is the mail-relevant case: verification tokens from unrelated services
// live alongside SPF at the zone apex and must not be disturbed.
func allowsCoexistence(recType string) bool {
	switch strings.ToUpper(recType) {
	case "TXT", "MX", "TLSA":
		return true
	default:
		return false
	}
}
