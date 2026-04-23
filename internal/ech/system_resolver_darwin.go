// See LICENSE file in the project root for license information.

//go:build darwin

package ech

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	scutildns "github.com/johnstarich/go/dns/scutil"
)

const resolverCacheTTL = time.Minute

type resolverSnapshot struct {
	resolvers []scutildns.Resolver
	expiresAt time.Time
}

var darwinResolverCache struct {
	mu       sync.Mutex
	snapshot resolverSnapshot
}

func systemNameservers(ctx context.Context, qname string) ([]string, error) {
	darwinResolverCache.mu.Lock()
	snapshot := darwinResolverCache.snapshot
	if time.Now().Before(snapshot.expiresAt) {
		darwinResolverCache.mu.Unlock()
		return nameserversFromResolvers(snapshot.resolvers, qname), nil
	}
	darwinResolverCache.mu.Unlock()
	config, err := scutildns.ReadMacOSDNS(ctx)
	if err != nil {
		return nil, fmt.Errorf("read macOS DNS configuration: %w", err)
	}
	darwinResolverCache.mu.Lock()
	darwinResolverCache.snapshot = resolverSnapshot{
		resolvers: append([]scutildns.Resolver(nil), config.Resolvers...),
		expiresAt: time.Now().Add(resolverCacheTTL),
	}
	darwinResolverCache.mu.Unlock()
	return nameserversFromResolvers(config.Resolvers, qname), nil
}

func fallbackNameservers(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func nameserversFromResolvers(resolvers []scutildns.Resolver, qname string) []string {
	host := normalizeDNSName(qname)
	bestMatch := -1
	out := make([]string, 0, 2)
	defaultResolvers := make([]string, 0, 2)
	for _, resolver := range resolvers {
		if len(resolver.Nameservers) == 0 {
			continue
		}
		nameservers := appendResolverNameservers(nil, resolver)
		if len(nameservers) == 0 {
			continue
		}
		domain := normalizeDNSName(resolver.Domain)
		if domain == "" {
			defaultResolvers = append(defaultResolvers, nameservers...)
			continue
		}
		if host != domain && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		if len(domain) < bestMatch {
			continue
		}
		if len(domain) > bestMatch {
			out = out[:0]
			bestMatch = len(domain)
		}
		out = append(out, nameservers...)
	}
	if len(out) > 0 {
		return out
	}
	return defaultResolvers
}

func appendResolverNameservers(dst []string, resolver scutildns.Resolver) []string {
	port := "53"
	if resolver.Port > 0 {
		port = strconv.Itoa(resolver.Port)
	}
	for _, nameserver := range resolver.Nameservers {
		trimmed := strings.TrimSpace(nameserver)
		if trimmed == "" {
			continue
		}
		dst = append(dst, ensurePort(trimmed, port))
	}
	return dst
}
