// See LICENSE file in the project root for license information.

package webtty

import (
	"testing"

	rstream "github.com/rstreamlabs/rstream-go"
)

func TestServerTransportDiscovery(t *testing.T) {
	for _, transport := range []WebTTYTransport{WebTTYTransportPlain, WebTTYTransportWebSocket, WebTTYTransportWebTransport} {
		for _, managed := range []bool{false, true} {
			for _, published := range []bool{false, true} {
				t.Run(string(transport)+"/"+testBoolName(managed)+"/"+testBoolName(published), func(t *testing.T) {
					props := rstream.TunnelProperties{ID: rstream.StringPtr("shell"), Publish: &published, Labels: map[string]string{WebTTYApplicationProtocolKey: WebTTYApplicationProtocol, "rstream.webtty.transport": string(transport)}}
					if managed {
						props.Protocol = rstream.ProtocolPtr(rstream.ProtocolWebTTY)
					}
					if transport == WebTTYTransportWebTransport {
						props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeDatagram)
					} else {
						props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
					}
					servers := ParseServers([]rstream.TunnelInventory{{TunnelProperties: props, Status: "online"}})
					if len(servers) != 1 {
						t.Fatalf("inventory lost WebTTY server: %v", servers)
					}
					for _, requested := range []WebTTYTransport{"", WebTTYTransportPlain, WebTTYTransportWebSocket, WebTTYTransportWebTransport} {
						got, err := servers[0].ResolveTransport(requested)
						if requested != "" && requested != transport {
							if err == nil {
								t.Fatalf("accepted incompatible transport %q for %q", requested, transport)
							}
						} else if err != nil || got != transport {
							t.Fatalf("ResolveTransport(%q) = %q, %v; want %q", requested, got, err, transport)
						}
					}
				})
			}
		}
	}
}

func TestServerTransportLegacyAndInvalidMetadata(t *testing.T) {
	tests := []struct {
		name      string
		labels    map[string]string
		kind      *rstream.TunnelType
		http      *rstream.HTTPVersion
		requested WebTTYTransport
		want      WebTTYTransport
		wantError bool
	}{
		{name: "legacy defaults to websocket", want: WebTTYTransportWebSocket},
		{name: "legacy plain override", requested: WebTTYTransportPlain, want: WebTTYTransportPlain},
		{name: "legacy datagram", kind: rstream.TunnelTypePtr(rstream.TunnelTypeDatagram), want: WebTTYTransportWebTransport},
		{name: "legacy datagram conflicting override", kind: rstream.TunnelTypePtr(rstream.TunnelTypeDatagram), requested: WebTTYTransportWebSocket, wantError: true},
		{name: "unknown label", labels: map[string]string{"rstream.webtty.transport": "future"}, wantError: true},
		{name: "empty label", labels: map[string]string{"rstream.webtty.transport": ""}, wantError: true},
		{name: "unknown override", requested: "future", wantError: true},
		{name: "websocket on datagrams", labels: map[string]string{"rstream.webtty.transport": "websocket"}, kind: rstream.TunnelTypePtr(rstream.TunnelTypeDatagram), wantError: true},
		{name: "webtransport on bytestream", labels: map[string]string{"rstream.webtty.transport": "webtransport"}, kind: rstream.TunnelTypePtr(rstream.TunnelTypeBytestream), wantError: true},
		{name: "datagram HTTP1", kind: rstream.TunnelTypePtr(rstream.TunnelTypeDatagram), http: rstream.HTTPVersionPtr(rstream.HTTP1_1), wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := rstream.TunnelProperties{ID: rstream.StringPtr("shell"), Protocol: rstream.ProtocolPtr(rstream.ProtocolWebTTY), Type: tt.kind, HTTPVersion: tt.http, Labels: tt.labels}
			servers := ParseServers([]rstream.TunnelInventory{{TunnelProperties: props, Status: "online"}})
			if len(servers) != 1 {
				t.Fatal("invalid transport must remain visible in inventory")
			}
			got, err := servers[0].ResolveTransport(tt.requested)
			if (err != nil) != tt.wantError || !tt.wantError && got != tt.want {
				t.Fatalf("ResolveTransport(%q) = %q, %v; want %q, error %v", tt.requested, got, err, tt.want, tt.wantError)
			}
		})
	}
}

func testBoolName(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
