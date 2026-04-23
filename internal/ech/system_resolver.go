// See LICENSE file in the project root for license information.

package ech

import (
	"context"
	"fmt"
	"strings"
)

func nameserversForQuery(ctx context.Context, qname string, opts ResolverOptions) ([]string, error) {
	if opts.DNSOverTLS && strings.TrimSpace(opts.DNSOverride) == "" {
		return nil, fmt.Errorf("DNS over TLS requires dns.override")
	}
	if opts.DNSOverride != "" {
		return []string{ensurePort(opts.DNSOverride, defaultResolverPort(opts))}, nil
	}
	nameservers, err := systemNameservers(ctx, qname)
	if err != nil {
		return nil, err
	}
	if len(nameservers) > 0 {
		return nameservers, nil
	}
	fallback, err := fallbackNameservers(ctx, qname)
	if err != nil {
		return nil, err
	}
	if len(fallback) == 0 {
		return nil, fmt.Errorf("no DNS resolvers configured")
	}
	return fallback, nil
}

func defaultResolverPort(opts ResolverOptions) string {
	if opts.DNSOverTLS {
		return "853"
	}
	return "53"
}
