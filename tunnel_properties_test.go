// See LICENSE file in the project root for license information.

package rstream

import (
	"strings"
	"testing"
)

func TestNormalizeCreateTunnelPropertiesPublishedTCP(t *testing.T) {
	props, err := normalizeCreateTunnelProperties(TunnelProperties{Protocol: ProtocolPtr(ProtocolTCP), Port: Uint32Ptr(10042)})
	if err != nil {
		t.Fatalf("normalizeCreateTunnelProperties() error = %v", err)
	}
	if props.Type == nil || *props.Type != TunnelTypeBytestream || props.Publish == nil || !*props.Publish {
		t.Fatalf("normalizeCreateTunnelProperties() = %#v", props)
	}
}

func TestNormalizeCreateTunnelPropertiesRejectsInvalidTCPOptions(t *testing.T) {
	tests := []struct {
		name    string
		props   TunnelProperties
		wantErr string
	}{
		{name: "port without tcp", props: TunnelProperties{Port: Uint32Ptr(10042)}, wantErr: "requires protocol tcp"},
		{name: "zero port", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolTCP), Port: Uint32Ptr(0)}, wantErr: "between 1 and 65535"},
		{name: "unpublished", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolTCP), Publish: BoolPtr(false)}, wantErr: "cannot be unpublished"},
		{name: "hostname", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolTCP), Hostname: StringPtr("ssh.example.test")}, wantErr: "do not accept"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeCreateTunnelProperties(tt.props)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("normalizeCreateTunnelProperties() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
