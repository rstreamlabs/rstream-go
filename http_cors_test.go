// See LICENSE file in the project root for license information.

package rstream

import (
	"reflect"
	"testing"

	"github.com/rstreamlabs/rstream-go/httpcors"
	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/proto"
)

func TestHTTPCORSProtocolRoundTrip(t *testing.T) {
	input := TunnelProperties{HTTPCORS: &httpcors.Config{AllowedOrigins: []string{"https://dashboard.example"}, AllowedMethods: []string{"PUT"}, AllowedHeaders: []string{"Authorization"}, ExposedHeaders: []string{"ETag"}, AllowCredentials: true, MaxAge: 120}}
	wire, err := proto.Marshal(toTunnelPropertiesPb(input))
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.TunnelProperties
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	output := toTunnelProperties(&decoded)
	if !reflect.DeepEqual(output.HTTPCORS, input.HTTPCORS) {
		t.Fatalf("CORS did not round trip: %+v", output.HTTPCORS)
	}
	copy := cloneTunnelProperties(input)
	copy.HTTPCORS.AllowedOrigins[0] = "https://changed.example"
	if input.HTTPCORS.AllowedOrigins[0] != "https://dashboard.example" {
		t.Fatal("clone shares mutable CORS configuration")
	}
	if toTunnelPropertiesPb(TunnelProperties{}).HttpCors != nil || toTunnelProperties(&pb.TunnelProperties{}).HTTPCORS != nil {
		t.Fatal("absent CORS must remain absent")
	}
}

func TestHTTPCORSRejectsNonHTTPAndPrivateTunnels(t *testing.T) {
	for _, input := range []TunnelProperties{{Publish: BoolPtr(false)}, {Protocol: ProtocolPtr(ProtocolTLS)}, {Protocol: ProtocolPtr(ProtocolTCP)}} {
		input.HTTPCORS = &httpcors.Config{AllowedOrigins: []string{"https://dashboard.example"}}
		if _, err := normalizeCreateTunnelProperties(input); err == nil {
			t.Fatalf("accepted CORS for unsupported tunnel: %+v", input)
		}
	}
}
