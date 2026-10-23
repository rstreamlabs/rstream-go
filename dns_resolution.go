// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"fmt"
	"net"
	"net/netip"

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
	addresses, err := resolveDialAddresses(ctx, addr, cfg)
	if err != nil {
		return "", err
	}
	return addresses[0], nil
}

func resolveDialAddresses(ctx context.Context, addr string, cfg dnsResolverConfig) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to split host:port from %q: %w", addr, err)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if cfg.forceIPv4 && !ip.Unmap().Is4() {
			return nil, fmt.Errorf("address %q is not IPv4", host)
		}
		if cfg.forceIPv6 && !ip.Unmap().Is6() {
			return nil, fmt.Errorf("address %q is not IPv6", host)
		}
		return []string{addr}, nil
	}
	var addresses []string
	if cfg.enabled() {
		addresses, err = dnslookup.LookupHost(ctx, host, dnslookup.ResolverOptions{DNSOverride: cfg.override, DNSOverTLS: cfg.overTLS, DNSServerName: cfg.serverName, DNSSECEnabled: cfg.dnssecEnabled, ForceIPv4: cfg.forceIPv4, ForceIPv6: cfg.forceIPv6})
	} else {
		var ips []net.IPAddr
		ips, err = net.DefaultResolver.LookupIPAddr(ctx, host)
		for _, ip := range ips {
			addresses = append(addresses, ip.String())
		}
	}
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed for %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("DNS lookup returned no addresses for %q", host)
	}
	// Alternate families without changing order within either family.
	families := [2][]string{}
	firstIPv6 := false
	seen := make(map[string]bool, len(addresses))
	for _, value := range addresses {
		ip, err := netip.ParseAddr(value)
		if err != nil {
			return nil, fmt.Errorf("DNS lookup returned invalid address %q", value)
		}
		ip = ip.Unmap()
		if seen[ip.String()] {
			continue
		}
		seen[ip.String()] = true
		if len(seen) == 1 {
			firstIPv6 = ip.Is6()
		}
		family := 0
		if ip.Is6() != firstIPv6 {
			family = 1
		}
		families[family] = append(families[family], net.JoinHostPort(ip.String(), port))
	}
	out := make([]string, 0, len(seen))
	for i := 0; i < max(len(families[0]), len(families[1])); i++ {
		for _, family := range families {
			if i < len(family) {
				out = append(out, family[i])
			}
		}
	}
	return out, nil
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
