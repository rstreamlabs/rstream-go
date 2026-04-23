// See LICENSE file in the project root for license information.

package config

import (
	"testing"

	"github.com/rstreamlabs/rstream-go"
)

func TestMergeTransportSafeOverride(t *testing.T) {
	base := &TransportConfig{
		IPFamily: "ipv6",
		DNS:      &DNSConfig{Override: "1.1.1.1:53", TLS: rstream.BoolPtr(true), ServerName: "dns.example.com", DNSSEC: rstream.BoolPtr(true)},
		Proxy: &ProxyConfig{
			HTTP:    "http://proxy.local:3128",
			Headers: map[string]string{"X-Company": "acme"},
		},
	}
	override := &TransportConfig{
		DNS: &DNSConfig{Override: ""},
		Proxy: &ProxyConfig{
			Headers: map[string]string{"X-Env": "ci"},
		},
	}
	merged := MergeTransport(base, override)
	if merged.IPFamily != "ipv6" {
		t.Fatalf("expected IP family preserved, got %q", merged.IPFamily)
	}
	if merged.DNS == nil || merged.DNS.Override != "1.1.1.1:53" {
		t.Fatalf("expected DNS override preserved, got %+v", merged.DNS)
	}
	if merged.DNS.TLS == nil || !*merged.DNS.TLS || merged.DNS.ServerName != "dns.example.com" || merged.DNS.DNSSEC == nil || !*merged.DNS.DNSSEC {
		t.Fatalf("expected DNS advanced settings preserved, got %+v", merged.DNS)
	}
	if merged.Proxy == nil || merged.Proxy.HTTP != "http://proxy.local:3128" {
		t.Fatalf("expected proxy HTTP preserved, got %+v", merged.Proxy)
	}
	if merged.Proxy.Headers["X-Company"] != "acme" || merged.Proxy.Headers["X-Env"] != "ci" {
		t.Fatalf("expected headers merged, got %+v", merged.Proxy.Headers)
	}
}
