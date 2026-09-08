package main

import (
	"context"

	"lambdamail/protocols/internal/infrastructure/postgres"
	httppresentation "lambdamail/protocols/internal/presentation/http"
)

// bimiStore adapts the repository to the shape the HTTP layer asks for, so
// that layer does not import the database package.
type bimiStore struct {
	repo *postgres.BimiRepository
}

func (s bimiStore) Save(ctx context.Context, domain string, svg []byte, vmcURL string) (string, error) {
	return s.repo.Save(ctx, domain, svg, vmcURL)
}

func (s bimiStore) Load(ctx context.Context, domain string) (*httppresentation.BimiLogo, error) {
	logo, err := s.repo.Load(ctx, domain)
	if err != nil || logo == nil {
		return nil, err
	}
	return &httppresentation.BimiLogo{SVG: logo.SVG, ETag: logo.ETag, VmcURL: logo.VmcURL}, nil
}

func (s bimiStore) Delete(ctx context.Context, domain string) error {
	return s.repo.Delete(ctx, domain)
}
