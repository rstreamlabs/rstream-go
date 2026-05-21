// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"fmt"
	"net"

	dnslookup "github.com/rstreamlabs/rstream-go/internal/ech"
)

type dnsResolverConfig struct {
	override      string
	overTLS       bool
	serverName    string
	dnssecEnabled bool
	forceIPv4     bool
	forceIPv6     bool
}

func (c dnsResolverConfig) enabled() bool {
	return c.override != "" || c.overTLS || c.dnssecEnabled || c.forceIPv4 || c.forceIPv6
}

func resolveDialAddress(ctx context.Context, addr string, cfg dnsResolverConfig) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("failed to split host:port from %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); ip != nil {
		if cfg.forceIPv4 && ip.To4() == nil {
			return "", fmt.Errorf("address %q is not IPv4", host)
		}
		if cfg.forceIPv6 && (ip.To16() == nil || ip.To4() != nil) {
			return "", fmt.Errorf("address %q is not IPv6", host)
		}
		return net.JoinHostPort(host, port), nil
	}
	addrs, err := dnslookup.LookupHost(ctx, host, dnslookup.ResolverOptions{
		DNSOverride:   cfg.override,
		DNSOverTLS:    cfg.overTLS,
		DNSServerName: cfg.serverName,
		DNSSECEnabled: cfg.dnssecEnabled,
		ForceIPv4:     cfg.forceIPv4,
		ForceIPv6:     cfg.forceIPv6,
	})
	if err != nil {
		return "", fmt.Errorf("DNS lookup failed for %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("DNS lookup returned no addresses for %q", host)
	}
	return net.JoinHostPort(addrs[0], port), nil
}

func dnsResolverOptionsFromTransport(t *Transport) dnsResolverConfig {
	return dnsResolverConfig{
		override:      stringValue(t.DNSOverride),
		overTLS:       boolValue(t.DNSOverTLS),
		serverName:    stringValue(t.DNSServerName),
		dnssecEnabled: boolValue(t.DNSSECEnabled),
		forceIPv4:     boolValue(t.ForceIPv4),
		forceIPv6:     boolValue(t.ForceIPv6),
	}
}

func dnsResolverOptionsFromQUICTransport(t *QUICTransport) dnsResolverConfig {
	return dnsResolverConfig{
		override:      stringValue(t.DNSOverride),
		overTLS:       boolValue(t.DNSOverTLS),
		serverName:    stringValue(t.DNSServerName),
		dnssecEnabled: boolValue(t.DNSSECEnabled),
		forceIPv4:     boolValue(t.ForceIPv4),
		forceIPv6:     boolValue(t.ForceIPv6),
	}
}
