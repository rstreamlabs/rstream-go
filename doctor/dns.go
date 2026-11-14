// See LICENSE file in the project root for license information.

package doctor

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	dnslookup "github.com/rstreamlabs/rstream-go/internal/ech"
)

func checkDoctorDNS(ctx context.Context, report *Report, transport rstream.Dialer, host, selected string) {
	if auto, ok := transport.(*rstream.AutoTransport); ok && auto != nil && selected == "quic" {
		transport = auto.QUIC
	}
	configured, ok := doctorTLSTransport(transport)
	if !ok {
		report.add("dns", StatusSkip, "DNS resolution is owned by the configured custom transport", map[string]string{"host": host})
		return
	}
	opts := dnslookup.ResolverOptions{}
	if configured.DNSOverride != nil {
		opts.DNSOverride = *configured.DNSOverride
	}
	if configured.DNSOverTLS != nil {
		opts.DNSOverTLS = *configured.DNSOverTLS
	}
	if configured.DNSServerName != nil {
		opts.DNSServerName = *configured.DNSServerName
	}
	if configured.DNSSECEnabled != nil {
		opts.DNSSECEnabled = *configured.DNSSECEnabled
	}
	if configured.ForceIPv4 != nil {
		opts.ForceIPv4 = *configured.ForceIPv4
	}
	if configured.ForceIPv6 != nil {
		opts.ForceIPv6 = *configured.ForceIPv6
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := lookupDoctorHost(lookupCtx, host, opts)
	if err != nil {
		report.add("dns", StatusFail, err.Error(), map[string]string{"host": host})
		return
	}
	report.add("dns", StatusPass, "engine host resolves", map[string]string{"host": host, "addresses": strings.Join(ips, ",")})
}

func lookupDoctorHost(ctx context.Context, host string, opts dnslookup.ResolverOptions) ([]string, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if opts.ForceIPv4 && !ip.Unmap().Is4() || opts.ForceIPv6 && !ip.Unmap().Is6() {
			return nil, fmt.Errorf("address %q does not match the configured IP family", host)
		}
		return []string{host}, nil
	}
	if opts.DNSOverride == "" && !opts.DNSOverTLS && !opts.DNSSECEnabled && !opts.ForceIPv4 && !opts.ForceIPv6 {
		return net.DefaultResolver.LookupHost(ctx, host)
	}
	ips, err := dnslookup.LookupHost(ctx, host, opts)
	if err == nil && len(ips) == 0 {
		return nil, fmt.Errorf("DNS lookup returned no addresses for %q", host)
	}
	return ips, err
}
