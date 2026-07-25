package port

import (
	"context"
)

type MXResolver interface {
	LookupMX(ctx context.Context, domain string) ([]string, error)
}
