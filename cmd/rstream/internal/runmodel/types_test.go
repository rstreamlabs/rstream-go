// See LICENSE file in the project root for license information.

package runmodel

import (
	"reflect"
	"testing"

	"github.com/rstreamlabs/rstream-go"
)

func TestParseForwardTarget(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		defaultHost string
		want        ForwardTarget
		wantErr     bool
	}{
		{name: "port with default host", raw: "8080", defaultHost: "localhost", want: ForwardTarget{Host: "localhost", Port: "8080"}},
		{name: "explicit host", raw: " 127.0.0.1:9000 ", defaultHost: "localhost", want: ForwardTarget{Host: "127.0.0.1", Port: "9000"}},
		{name: "empty raw", raw: " ", defaultHost: "localhost", wantErr: true},
		{name: "missing host", raw: ":8080", defaultHost: "localhost", wantErr: true},
		{name: "missing port", raw: "localhost:", defaultHost: "localhost", wantErr: true},
		{name: "missing default host", raw: "8080", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseForwardTarget(tt.raw, tt.defaultHost)
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
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
			if got.String() != got.Host+":"+got.Port {
				t.Fatalf("unexpected target string: %q", got.String())
			}
		})
	}
}

func TestApplyManagedLabelsAndSanitizeName(t *testing.T) {
	ApplyManagedLabels(nil, "docker")
	props := rstream.TunnelProperties{Labels: map[string]string{"keep": "value"}}
	ApplyManagedLabels(&props, " docker ")
	if !reflect.DeepEqual(props.Labels, map[string]string{
		"keep":         "value",
		ManagedByLabel: "run",
		SourceLabel:    " docker ",
	}) {
		t.Fatalf("unexpected labels: %#v", props.Labels)
	}
	props = rstream.TunnelProperties{}
	ApplyManagedLabels(&props, " ")
	if !reflect.DeepEqual(props.Labels, map[string]string{ManagedByLabel: "run"}) {
		t.Fatalf("unexpected blank-source labels: %#v", props.Labels)
	}
	if got := SanitizeName(" Demo App_1.2/Blue "); got != "demo-app_1.2-blue" {
		t.Fatalf("unexpected sanitized name: %q", got)
	}
	if got := SanitizeName("   "); got != "" {
		t.Fatalf("blank name should sanitize to empty, got %q", got)
	}
}

func TestSummaryIncludesOptionalProperties(t *testing.T) {
	d := DesiredTunnel{
		Name:    "demo",
		Forward: ForwardTarget{Host: "localhost", Port: "8080"},
		Context: ResolvedContext{Engine: "engine.example.com:443"},
		Props: rstream.TunnelProperties{
			Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP),
			Type:     rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
			Publish:  rstream.BoolPtr(true),
		},
	}
	got := Summary(d)
	want := map[string]any{
		"name":     "demo",
		"forward":  "localhost:8080",
		"engine":   "engine.example.com:443",
		"protocol": rstream.ProtocolHTTP,
		"type":     rstream.TunnelTypeBytestream,
		"publish":  true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
