// See LICENSE file in the project root for license information.

package rstream

import (
	"net"
	"strings"
	"testing"
)

func TestFormatForwardedHostPortShowsUpstreamTLSForPublishedProtocols(t *testing.T) {
	upstreamTLS := true
	tests := []struct {
		name     string
		props    TunnelProperties
		expected string
	}{
		{name: "tls", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolTLS), UpstreamTLS: &upstreamTLS}, expected: "127.0.0.1:8443 (tls)"},
		{name: "dtls", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolDTLS), UpstreamTLS: &upstreamTLS}, expected: "127.0.0.1:8443 (dtls)"},
	}
	for _, tt := range tests {
		got, err := FormatForwardedHostPort("127.0.0.1", "8443", tt.props)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.name, err)
		}
		if got != tt.expected {
			t.Fatalf("%s: expected %q, got %q", tt.name, tt.expected, got)
		}
	}
}

func TestFormatForwardingAddrVariants(t *testing.T) {
	tests := []struct {
		name    string
		props   TunnelProperties
		want    string
		wantErr bool
	}{
		{
			name:  "published http default port",
			props: TunnelProperties{Hostname: StringPtr(" app.example.com "), Protocol: ProtocolPtr(ProtocolHTTP)},
			want:  "https://app.example.com",
		},
		{
			name:  "published tls includes port",
			props: TunnelProperties{Hostname: StringPtr("tls.example.com"), Port: Uint32Ptr(443), Protocol: ProtocolPtr(ProtocolTLS)},
			want:  "tls.example.com:443 (tls)",
		},
		{
			name:  "published non default port",
			props: TunnelProperties{Hostname: StringPtr("app.example.com"), Port: Uint32Ptr(8443), Protocol: ProtocolPtr(ProtocolHTTP)},
			want:  "https://app.example.com:8443",
		},
		{
			name:  "legacy host wins without formatting",
			props: TunnelProperties{Host: StringPtr("legacy.example.com"), Protocol: ProtocolPtr(ProtocolQUIC)},
			want:  "legacy.example.com (quic)",
		},
		{
			name:  "unpublished name",
			props: TunnelProperties{Name: StringPtr("local-app")},
			want:  "rstrm://local-app (unpublished)",
		},
		{
			name:  "unpublished id fallback",
			props: TunnelProperties{ID: StringPtr("tun_123")},
			want:  "rstrm://tun_123 (unpublished)",
		},
		{
			name:    "empty properties",
			props:   TunnelProperties{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatForwardingAddr(tt.props)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatForwardedHostPortVariants(t *testing.T) {
	tests := []struct {
		name  string
		host  string
		port  string
		props TunnelProperties
		want  string
	}{
		{name: "http hides port 80", host: "127.0.0.1", port: "80", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolHTTP), HTTPVersion: HTTPVersionPtr(HTTP1_1)}, want: "http://127.0.0.1 (http/1.1)"},
		{name: "h2c shows non default port", host: "127.0.0.1", port: "8080", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolHTTP), HTTPVersion: HTTPVersionPtr(HTTP2)}, want: "http://127.0.0.1:8080 (h2c)"},
		{name: "h3 forces https marker", host: "127.0.0.1", port: "443", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolHTTP), HTTPVersion: HTTPVersionPtr(HTTP3)}, want: "https://127.0.0.1 (h3)"},
		{name: "http upstream tls hides 443", host: "127.0.0.1", port: "443", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolHTTP), HTTPUseTLS: BoolPtr(true), HTTPVersion: HTTPVersionPtr(HTTP1_1)}, want: "https://127.0.0.1"},
		{name: "tls terminated reports tcp", host: "127.0.0.1", port: "9000", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolTLS), TLSMode: TLSModePtr(TLSModeTerminated)}, want: "127.0.0.1:9000 (tcp)"},
		{name: "dtls without upstream tls reports udp", host: "127.0.0.1", port: "9000", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolDTLS), UpstreamTLS: BoolPtr(false)}, want: "127.0.0.1:9000 (udp)"},
		{name: "quic marker", host: "127.0.0.1", port: "4433", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolQUIC)}, want: "127.0.0.1:4433 (quic)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatForwardedHostPort(tt.host, tt.port, tt.props)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatForwardedAddrValidation(t *testing.T) {
	if _, err := FormatForwardedAddr(net.TCPAddr{}, TunnelProperties{}); err == nil || !strings.Contains(err.Error(), "no IP") {
		t.Fatalf("expected no IP error, got %v", err)
	}
	if _, err := FormatForwardedAddr(net.TCPAddr{IP: net.ParseIP("127.0.0.1")}, TunnelProperties{}); err == nil || !strings.Contains(err.Error(), "no Port") {
		t.Fatalf("expected no Port error, got %v", err)
	}
	got, err := FormatForwardedAddr(net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}, TunnelProperties{Protocol: ProtocolPtr(ProtocolHTTP)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "http://127.0.0.1:8080" {
		t.Fatalf("got %q", got)
	}
}

func TestSplitHostPort(t *testing.T) {
	host, port, err := splitHostPort("example.com:443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host == nil || *host != "example.com" || port == nil || *port != "443" {
		t.Fatalf("unexpected host/port: %v %v", host, port)
	}
	host, port, err = splitHostPort("example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host == nil || *host != "example.com" || port != nil {
		t.Fatalf("unexpected bare host result: %v %v", host, port)
	}
	if _, _, err := splitHostPort("example.com:bad:addr"); err == nil {
		t.Fatalf("expected invalid host:port error")
	}
}
