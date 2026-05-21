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
			HTTP:            "http://proxy.local:3128",
			SOCKS5:          "socks5://socks.local:1080",
			Headers:         map[string]string{"X-Company": "acme"},
			FromEnvironment: rstream.BoolPtr(false),
		},
	}
	override := &TransportConfig{
		DNS: &DNSConfig{Override: ""},
		Proxy: &ProxyConfig{
			Headers:         map[string]string{"X-Env": "ci"},
			FromEnvironment: rstream.BoolPtr(true),
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
	if merged.Proxy.SOCKS5 != "socks5://socks.local:1080" {
		t.Fatalf("expected proxy SOCKS5 preserved, got %+v", merged.Proxy)
	}
	if merged.Proxy.Headers["X-Company"] != "acme" || merged.Proxy.Headers["X-Env"] != "ci" {
		t.Fatalf("expected headers merged, got %+v", merged.Proxy.Headers)
	}
	if merged.Proxy.FromEnvironment == nil || !*merged.Proxy.FromEnvironment {
		t.Fatalf("expected proxy fromEnvironment override, got %+v", merged.Proxy)
	}
}

func TestMergeTransportDeepCopiesNestedValues(t *testing.T) {
	base := &TransportConfig{
		Bind: &BindConfig{Mode: "address", Address: "127.0.0.1"},
		DNS:  &DNSConfig{Override: "1.1.1.1:53"},
		Proxy: &ProxyConfig{
			HTTP:    "http://proxy.local:3128",
			Headers: map[string]string{"X-Trace": "source"},
		},
	}
	merged := MergeTransport(base, nil)
	base.Bind.Address = "10.0.0.1"
	base.DNS.Override = "8.8.8.8:53"
	base.Proxy.Headers["X-Trace"] = "mutated"
	if merged.Bind.Address != "127.0.0.1" || merged.DNS.Override != "1.1.1.1:53" || merged.Proxy.Headers["X-Trace"] != "source" {
		t.Fatalf("merged transport should be independent from base: %#v", merged)
	}
}

func TestFlattenTransportBuildsTCPTransport(t *testing.T) {
	cfg := &TransportConfig{
		Bind:     &BindConfig{Mode: "interface", Interface: "lo0"},
		IPFamily: "ipv4",
		DNS:      &DNSConfig{Override: "1.1.1.1:853", TLS: rstream.BoolPtr(true), ServerName: "cloudflare-dns.com", DNSSEC: rstream.BoolPtr(true)},
		MPTCP:    rstream.BoolPtr(true),
		Proxy: &ProxyConfig{
			HTTP:            "https://proxy.local:8443",
			SOCKS5:          "socks5://socks.local:1080",
			Username:        "user",
			Password:        "pass",
			Headers:         map[string]string{"X-Trace": "abc"},
			FromEnvironment: rstream.BoolPtr(true),
		},
	}
	transport, ok := FlattenTransport(cfg).(*rstream.Transport)
	if !ok {
		t.Fatalf("FlattenTransport() returned %T, want *rstream.Transport", FlattenTransport(cfg))
	}
	if transport.NetworkInterface == nil || *transport.NetworkInterface != "lo0" || transport.ForceIPv4 == nil || !*transport.ForceIPv4 {
		t.Fatalf("bind/IP settings not flattened: %#v", transport)
	}
	if transport.DNSOverride == nil || *transport.DNSOverride != "1.1.1.1:853" || transport.DNSOverTLS == nil || !*transport.DNSOverTLS {
		t.Fatalf("DNS settings not flattened: %#v", transport)
	}
	if transport.DNSServerName == nil || *transport.DNSServerName != "cloudflare-dns.com" || transport.DNSSECEnabled == nil || !*transport.DNSSECEnabled {
		t.Fatalf("advanced DNS settings not flattened: %#v", transport)
	}
	if transport.MPTCPEnabled == nil || !*transport.MPTCPEnabled {
		t.Fatalf("MPTCP setting not flattened")
	}
	if transport.ProxyHTTP == nil || *transport.ProxyHTTP != "https://proxy.local:8443" || transport.ProxySOCKS5 == nil || *transport.ProxySOCKS5 != "socks5://socks.local:1080" || transport.ProxyUsername == nil || *transport.ProxyUsername != "user" {
		t.Fatalf("proxy settings not flattened: %#v", transport)
	}
	if transport.ProxyFromEnvironment == nil || !*transport.ProxyFromEnvironment {
		t.Fatalf("proxy fromEnvironment not flattened: %#v", transport)
	}
	if transport.ProxyHTTPHeaders["X-Trace"] != "abc" {
		t.Fatalf("proxy headers not flattened: %#v", transport.ProxyHTTPHeaders)
	}
	cfg.Proxy.Headers["X-Trace"] = "mutated"
	if transport.ProxyHTTPHeaders["X-Trace"] != "abc" {
		t.Fatalf("flattened proxy headers should be copied")
	}
}

func TestFlattenTransportBuildsQUICTransport(t *testing.T) {
	cfg := &TransportConfig{
		UseQUIC:  rstream.BoolPtr(true),
		Bind:     &BindConfig{Mode: "address", Address: "127.0.0.1"},
		IPFamily: "ipv6",
		DNS:      &DNSConfig{Override: "1.1.1.1:853", TLS: rstream.BoolPtr(true), ServerName: "cloudflare-dns.com", DNSSEC: rstream.BoolPtr(true)},
		Proxy:    &ProxyConfig{HTTP: "https://masque.local:443", SOCKS5: "socks5://socks.local:1080", Headers: map[string]string{"X-Trace": "abc"}, FromEnvironment: rstream.BoolPtr(true)},
	}
	transport, ok := FlattenTransport(cfg).(*rstream.QUICTransport)
	if !ok {
		t.Fatalf("FlattenTransport() returned %T, want *rstream.QUICTransport", FlattenTransport(cfg))
	}
	if transport.LocalAddr == nil || *transport.LocalAddr != "127.0.0.1" || transport.ForceIPv6 == nil || !*transport.ForceIPv6 {
		t.Fatalf("bind/IP settings not flattened: %#v", transport)
	}
	if transport.DNSOverride == nil || *transport.DNSOverride != "1.1.1.1:853" || transport.DNSOverTLS == nil || !*transport.DNSOverTLS {
		t.Fatalf("DNS settings not flattened: %#v", transport)
	}
	if transport.DNSServerName == nil || *transport.DNSServerName != "cloudflare-dns.com" || transport.DNSSECEnabled == nil || !*transport.DNSSECEnabled {
		t.Fatalf("advanced DNS settings not flattened: %#v", transport)
	}
	if transport.ProxyHTTP == nil || *transport.ProxyHTTP != "https://masque.local:443" || transport.ProxySOCKS5 == nil || *transport.ProxySOCKS5 != "socks5://socks.local:1080" {
		t.Fatalf("proxy settings not flattened: %#v", transport)
	}
	if transport.ProxyFromEnvironment == nil || !*transport.ProxyFromEnvironment {
		t.Fatalf("proxy fromEnvironment not flattened: %#v", transport)
	}
	if transport.ProxyHTTPHeaders["X-Trace"] != "abc" {
		t.Fatalf("proxy headers not flattened: %#v", transport.ProxyHTTPHeaders)
	}
}
