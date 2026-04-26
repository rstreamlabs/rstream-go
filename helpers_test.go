// See LICENSE file in the project root for license information.

package rstream

import "testing"

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
