package netdns

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
)

type NetMXResolver struct{}

func NewNetMXResolver() *NetMXResolver {
	return &NetMXResolver{}
}

func (r *NetMXResolver) LookupMX(ctx context.Context, domain string) ([]string, error) {
	mxs, err := net.DefaultResolver.LookupMX(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("lookup mx for %s: %w", domain, err)
	}

	sort.Slice(mxs, func(i, j int) bool {
		return mxs[i].Pref < mxs[j].Pref
	})

	var hosts []string
	for _, mx := range mxs {
		h := strings.TrimSuffix(mx.Host, ".")
		if h != "" {
			hosts = append(hosts, h)
		}
	}

	if len(hosts) == 0 {
		hosts = []string{domain}
	}

	return hosts, nil
}
