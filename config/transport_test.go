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
		TLS:      &TLSConfig{CAFile: "/base/engine-ca.pem", ServerName: "base.engine.local"},
		Proxy: &ProxyConfig{
			HTTP:            "http://proxy.local:3128",
			SOCKS5:          "socks5://socks.local:1080",
			Headers:         map[string]string{"X-Company": "acme"},
			FromEnvironment: rstream.BoolPtr(false),
			TLS:             &ProxyTLSConfig{CAFile: "/base/ca.pem", ServerName: "base.proxy.local"},
		},
	}
	override := &TransportConfig{
		DNS: &DNSConfig{Override: ""},
		TLS: &TLSConfig{ServerName: "override.engine.local"},
		Proxy: &ProxyConfig{
			Headers:         map[string]string{"X-Env": "ci"},
			FromEnvironment: rstream.BoolPtr(true),
			TLS:             &ProxyTLSConfig{ServerName: "override.proxy.local", InsecureSkipVerify: rstream.BoolPtr(true)},
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
	if merged.TLS == nil || merged.TLS.CAFile != "/base/engine-ca.pem" || merged.TLS.ServerName != "override.engine.local" {
		t.Fatalf("expected engine TLS settings merged, got %+v", merged.TLS)
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
	if merged.Proxy.TLS == nil || merged.Proxy.TLS.CAFile != "/base/ca.pem" || merged.Proxy.TLS.ServerName != "override.proxy.local" || merged.Proxy.TLS.InsecureSkipVerify == nil || !*merged.Proxy.TLS.InsecureSkipVerify {
		t.Fatalf("expected proxy TLS settings merged, got %+v", merged.Proxy.TLS)
	}
}

func TestMergeTransportDeepCopiesNestedValues(t *testing.T) {
	base := &TransportConfig{
		Bind: &BindConfig{Mode: "address", Address: "127.0.0.1"},
		DNS:  &DNSConfig{Override: "1.1.1.1:53"},
		TLS:  &TLSConfig{ServerName: "engine.local"},
		Proxy: &ProxyConfig{
			HTTP:    "http://proxy.local:3128",
			Headers: map[string]string{"X-Trace": "source"},
			TLS:     &ProxyTLSConfig{ServerName: "source.proxy.local"},
		},
	}
	merged := MergeTransport(base, nil)
	base.Bind.Address = "10.0.0.1"
	base.DNS.Override = "8.8.8.8:53"
	base.TLS.ServerName = "mutated.engine.local"
	base.Proxy.Headers["X-Trace"] = "mutated"
	base.Proxy.TLS.ServerName = "mutated.proxy.local"
	if merged.Bind.Address != "127.0.0.1" || merged.DNS.Override != "1.1.1.1:53" || merged.TLS.ServerName != "engine.local" || merged.Proxy.Headers["X-Trace"] != "source" || merged.Proxy.TLS.ServerName != "source.proxy.local" {
		t.Fatalf("merged transport should be independent from base: %#v", merged)
	}
}

func TestEngineTLSConfig(t *testing.T) {
	certFile, _ := writeTestClientCertificate(t)
	tlsConfig, err := EngineTLSConfig(&TransportConfig{
		TLS: &TLSConfig{CAFile: certFile, ServerName: "engine.local"},
	})
	if err != nil {
		t.Fatalf("EngineTLSConfig() error = %v", err)
	}
	if tlsConfig == nil || tlsConfig.RootCAs == nil || tlsConfig.ServerName != "engine.local" {
		t.Fatalf("engine TLS config not built: %#v", tlsConfig)
	}
	_, err = EngineTLSConfig(&TransportConfig{
		TLS: &TLSConfig{CAFile: "/does/not/exist.pem"},
	})
	if err == nil {
		t.Fatalf("expected invalid engine CA file error")
	}
}

func TestFlattenTransportBuildsTCPTransport(t *testing.T) {
	certFile, _ := writeTestClientCertificate(t)
	cfg := &TransportConfig{
		Bind:     &BindConfig{Mode: "interface", Interface: "lo0"},
		IPFamily: "ipv4",
		DNS:      &DNSConfig{Override: "1.1.1.1:853", TLS: rstream.BoolPtr(true), ServerName: "cloudflare-dns.com", DNSSEC: rstream.BoolPtr(true)},
		MPTCP:    rstream.BoolPtr(true),
		Proxy: &ProxyConfig{
			HTTP:            "https://proxy.local:8443",
			Username:        "user",
			Password:        "pass",
			Headers:         map[string]string{"X-Trace": "abc"},
			FromEnvironment: rstream.BoolPtr(true),
			TLS:             &ProxyTLSConfig{CAFile: certFile, ServerName: "proxy.local"},
		},
	}
	dialer, err := FlattenTransportWithError(cfg)
	if err != nil {
		t.Fatalf("FlattenTransportWithError() error = %v", err)
	}
	transport, ok := dialer.(*rstream.Transport)
	if !ok {
		t.Fatalf("FlattenTransportWithError() returned %T, want *rstream.Transport", dialer)
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
	if transport.ProxyHTTP == nil || *transport.ProxyHTTP != "https://proxy.local:8443" || transport.ProxyUsername == nil || *transport.ProxyUsername != "user" {
		t.Fatalf("proxy settings not flattened: %#v", transport)
	}
	if transport.ProxyFromEnvironment == nil || !*transport.ProxyFromEnvironment {
		t.Fatalf("proxy fromEnvironment not flattened: %#v", transport)
	}
	if transport.ProxyHTTPHeaders["X-Trace"] != "abc" {
		t.Fatalf("proxy headers not flattened: %#v", transport.ProxyHTTPHeaders)
	}
	if transport.TLSProxyConfig == nil || transport.TLSProxyConfig.ServerName != "proxy.local" || transport.TLSProxyConfig.RootCAs == nil {
		t.Fatalf("proxy TLS settings not flattened: %#v", transport.TLSProxyConfig)
	}
	cfg.Proxy.Headers["X-Trace"] = "mutated"
	if transport.ProxyHTTPHeaders["X-Trace"] != "abc" {
		t.Fatalf("flattened proxy headers should be copied")
	}
}

func TestFlattenTransportBuildsQUICTransport(t *testing.T) {
	certFile, _ := writeTestClientCertificate(t)
	cfg := &TransportConfig{
		UseQUIC:  rstream.BoolPtr(true),
		Bind:     &BindConfig{Mode: "address", Address: "127.0.0.1"},
		IPFamily: "ipv6",
		DNS:      &DNSConfig{Override: "1.1.1.1:853", TLS: rstream.BoolPtr(true), ServerName: "cloudflare-dns.com", DNSSEC: rstream.BoolPtr(true)},
		Proxy:    &ProxyConfig{HTTP: "https://masque.local:443", Headers: map[string]string{"X-Trace": "abc"}, FromEnvironment: rstream.BoolPtr(true), TLS: &ProxyTLSConfig{CAFile: certFile, ServerName: "masque.local"}},
	}
	dialer, err := FlattenTransportWithError(cfg)
	if err != nil {
		t.Fatalf("FlattenTransportWithError() error = %v", err)
	}
	transport, ok := dialer.(*rstream.QUICTransport)
	if !ok {
		t.Fatalf("FlattenTransportWithError() returned %T, want *rstream.QUICTransport", dialer)
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
	if transport.ProxyHTTP == nil || *transport.ProxyHTTP != "https://masque.local:443" {
		t.Fatalf("proxy settings not flattened: %#v", transport)
	}
	if transport.ProxyFromEnvironment == nil || !*transport.ProxyFromEnvironment {
		t.Fatalf("proxy fromEnvironment not flattened: %#v", transport)
	}
	if transport.ProxyHTTPHeaders["X-Trace"] != "abc" {
		t.Fatalf("proxy headers not flattened: %#v", transport.ProxyHTTPHeaders)
	}
	if transport.TLSProxyConfig == nil || transport.TLSProxyConfig.ServerName != "masque.local" || transport.TLSProxyConfig.RootCAs == nil {
		t.Fatalf("proxy TLS settings not flattened: %#v", transport.TLSProxyConfig)
	}
}

func TestFlattenTransportRejectsInvalidProxyCAFile(t *testing.T) {
	cfg := &TransportConfig{Proxy: &ProxyConfig{HTTP: "https://proxy.local:8443", TLS: &ProxyTLSConfig{CAFile: "/does/not/exist.pem"}}}
	if _, err := FlattenTransportWithError(cfg); err == nil {
		t.Fatalf("expected invalid proxy CA file error")
	}
}

func TestFlattenTransportRejectsStandaloneProxyTLSConfig(t *testing.T) {
	cfg := &TransportConfig{Proxy: &ProxyConfig{TLS: &ProxyTLSConfig{ServerName: "proxy.local"}}}
	if _, err := FlattenTransportWithError(cfg); err == nil {
		t.Fatalf("expected standalone proxy TLS error")
	}
	cfg = &TransportConfig{Proxy: &ProxyConfig{SOCKS5: "socks5://proxy.local:1080", TLS: &ProxyTLSConfig{ServerName: "proxy.local"}}}
	if _, err := FlattenTransportWithError(cfg); err == nil {
		t.Fatalf("expected SOCKS5 proxy TLS error")
	}
}
