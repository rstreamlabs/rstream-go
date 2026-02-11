// See LICENSE file in the project root for license information.

package config

import "testing"

func TestMergeTransportSafeOverride(t *testing.T) {
	base := &TransportConfig{
		IPFamily: "ipv6",
		DNS:      &DNSConfig{Override: "1.1.1.1:53"},
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
	if merged.Proxy == nil || merged.Proxy.HTTP != "http://proxy.local:3128" {
		t.Fatalf("expected proxy HTTP preserved, got %+v", merged.Proxy)
	}
	if merged.Proxy.Headers["X-Company"] != "acme" || merged.Proxy.Headers["X-Env"] != "ci" {
		t.Fatalf("expected headers merged, got %+v", merged.Proxy.Headers)
	}
}
