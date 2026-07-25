package port

import (
	"context"

	"lambdamail/protocols/internal/domain/valueobject"
)

type ScanInput struct {
	ClientIP   string
	HeloDomain string
	Sender     string
	Recipient  string
	Payload    []byte
}

type ContentScanner interface {
	Scan(ctx context.Context, input ScanInput) (*valueobject.ScanResult, error)
}
