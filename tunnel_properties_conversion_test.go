// See LICENSE file in the project root for license information.

package rstream

import (
	"reflect"
	"testing"
	"time"
)

func TestTunnelPropertiesProtoRoundTrip(t *testing.T) {
	created := time.Date(2026, 5, 7, 10, 30, 0, 0, time.UTC)
	props := TunnelProperties{
		ID:                         StringPtr("id-1"),
		CreationDate:               &created,
		Name:                       StringPtr("demo"),
		Type:                       TunnelTypePtr(TunnelTypeDatagram),
		Publish:                    BoolPtr(true),
		Protocol:                   ProtocolPtr(ProtocolHTTP),
		Labels:                     map[string]string{"env": "test"},
		GeoIP:                      []string{"FR", "US"},
		TrustedIPs:                 []string{"10.0.0.0/8"},
		Host:                       StringPtr("legacy.example.com"),
		Hostname:                   StringPtr("demo.example.com"),
		Port:                       Uint32Ptr(8443),
		TLSMode:                    TLSModePtr(TLSModeTerminated),
		TLSALPNs:                   []string{"h2"},
		TLSMinVersion:              StringPtr("tls1.3"),
		TLSCiphers:                 []string{"TLS_AES_128_GCM_SHA256"},
		MTLSAuth:                   BoolPtr(true),
		HTTPVersion:                HTTPVersionPtr(HTTP2),
		HTTPUseTLS:                 BoolPtr(true),
		UpstreamTLS:                BoolPtr(true),
		DatagramGuaranteedDelivery: BoolPtr(true),
		AllowCrossRegionRouting:    BoolPtr(true),
		TokenAuth:                  BoolPtr(true),
		RstreamAuth:                BoolPtr(false),
		ChallengeMode:              BoolPtr(true),
	}
	roundTrip := toTunnelProperties(toTunnelPropertiesPb(props))
	if !reflect.DeepEqual(roundTrip, props) {
		t.Fatalf("roundtrip mismatch:\n got: %#v\nwant: %#v", roundTrip, props)
	}
}
