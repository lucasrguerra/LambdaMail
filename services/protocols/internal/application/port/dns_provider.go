package port

import (
	"context"

	"lambdamail/protocols/internal/domain/entity"
)

// DnsProvider defines operations for managing remote DNS zone records (e.g. Cloudflare).
type DnsProvider interface {
	GetZoneID(ctx context.Context, domainName string) (string, error)
	ListRecords(ctx context.Context, zoneID string) ([]entity.DnsRecord, error)
	CreateRecord(ctx context.Context, zoneID string, record entity.DnsRecord) error
	UpdateRecord(ctx context.Context, zoneID string, record entity.DnsRecord) error
	DeleteRecord(ctx context.Context, zoneID string, recordID string) error
}
