// See LICENSE file in the project root for license information.

package rstream

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/rstreamlabs/rstream-go/pb"
)

func TestRawJSONAndAddr(t *testing.T) {
	raw := rawJSON(`{"ok":true}`)
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw json: %v", err)
	}
	if string(data) != `{"ok":true}` || raw.String() != `{"ok":true}` {
		t.Fatalf("unexpected raw json output: %s / %s", data, raw.String())
	}
	addr := (&Addr{IdOrName: "demo"}).String()
	if addr != "demo" {
		t.Fatalf("unexpected addr string: %q", addr)
	}
	if network := (&Addr{}).Network(); network != "rstrm" {
		t.Fatalf("unexpected network: %q", network)
	}
}

func TestPointerAndStringHelpers(t *testing.T) {
	if *StringPtr("value") != "value" {
		t.Fatalf("StringPtr failed")
	}
	if !*BoolPtr(true) {
		t.Fatalf("BoolPtr failed")
	}
	if *Uint16Ptr(16) != 16 || *Uint32Ptr(32) != 32 {
		t.Fatalf("integer pointer helpers failed")
	}
	if StrOrUndef(nil) != "undefined" || StrOrUndef(StringPtr("set")) != "set" {
		t.Fatalf("StrOrUndef returned unexpected values")
	}
}

func TestNetIPFromPbValue(t *testing.T) {
	if got := NetIPFromPbValue(nil); got != nil {
		t.Fatalf("nil protobuf IP should map to nil, got %v", got)
	}
	got := NetIPFromPbValue(&pb.IpAddress{Addr: &pb.IpAddress_V4{V4: 0x7f000001}})
	if !got.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("unexpected IPv4: %v", got)
	}
	got = NetIPFromPbValue(&pb.IpAddress{Addr: &pb.IpAddress_V6{V6: net.ParseIP("::1").To16()}})
	if !got.Equal(net.ParseIP("::1")) {
		t.Fatalf("unexpected IPv6: %v", got)
	}
}
