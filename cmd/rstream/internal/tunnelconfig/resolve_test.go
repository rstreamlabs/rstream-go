// See LICENSE file in the project root for license information.

package tunnelconfig

import (
	"reflect"
	"testing"

	"github.com/rstreamlabs/rstream-go"
)

func TestBoolPreservesPresenceAndInverseValues(t *testing.T) {
	for _, tc := range []struct {
		positive, inverse, want *bool
		wantErr                 bool
	}{
		{nil, nil, nil, false}, {rstream.BoolPtr(false), nil, rstream.BoolPtr(false), false},
		{rstream.BoolPtr(true), nil, rstream.BoolPtr(true), false},
		{nil, rstream.BoolPtr(true), rstream.BoolPtr(false), false},
		{nil, rstream.BoolPtr(false), rstream.BoolPtr(true), false},
		{rstream.BoolPtr(false), rstream.BoolPtr(false), nil, true},
	} {
		got, err := Bool(tc.positive, tc.inverse)
		if (err != nil) != tc.wantErr || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Bool(%v,%v)=%v,%v", tc.positive, tc.inverse, got, err)
		}
	}
}

func TestResolveIsIdempotentAndDoesNotMutateInput(t *testing.T) {
	original := rstream.TunnelProperties{Protocol: rstream.ProtocolPtr(" HTTP "), HTTPVersion: rstream.HTTPVersionPtr(" H2C "), TLSMode: rstream.TLSModePtr(" TERMINATED "), TLSMinVersion: rstream.StringPtr(" TLS1.3 "), Hostname: rstream.StringPtr(" app.example.com "), UpstreamTLS: rstream.BoolPtr(false)}
	first, err := Resolve(original)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Resolve(first)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("resolution is not idempotent: %#v %#v %v", first, second, err)
	}
	if *original.Protocol != " HTTP " || *original.Hostname != " app.example.com " || original.HTTPUseTLS != nil {
		t.Fatal("resolver mutated its input")
	}
	if *first.Protocol != rstream.ProtocolHTTP || *first.HTTPVersion != rstream.HTTP2 || *first.Hostname != "app.example.com" || first.UpstreamTLS == nil || *first.UpstreamTLS {
		t.Fatalf("unexpected resolved options: %#v", first)
	}
}
