// See LICENSE file in the project root for license information.

package rundocker

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

func TestDockerSourceAcceptsMTLSWithoutToken(t *testing.T) {
	identity := &tls.Config{GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &tls.Certificate{}, nil }}
	resolved := runmodel.ResolvedContext{Engine: "engine.example:443", TLSClientConfig: identity, CredentialID: "pin"}
	for _, named := range []bool{false, true} {
		labels := map[string]string{"rstream.tunnel.web.forward": "8080"}
		if named {
			labels["rstream.context"] = "device"
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("API-Version", "1.48")
			if strings.HasSuffix(r.URL.Path, "/_ping") {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]container.Summary{{ID: "fixture", Names: []string{"/fixture"}, Labels: labels, NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{"default": {IPAddress: netip.MustParseAddr("10.0.0.2")}}}}})
		}))
		t.Cleanup(server.Close)
		source, err := NewSource("tcp://"+server.Listener.Addr().String(), "", resolved, func(string) (runmodel.ResolvedContext, error) { return resolved, nil }, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = source.Close() })
		desired, err := source.List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(desired) != 1 || desired[0].Context.TLSClientConfig != identity || desired[0].Context.Token != "" || desired[0].Context.CredentialID != "pin" {
			t.Fatal("Docker discovery did not preserve its selected mTLS identity")
		}
	}
}

func TestContainerNameFallbacks(t *testing.T) {
	if got := containerName(container.Summary{Names: []string{"/web"}}); got != "web" {
		t.Fatalf("containerName(name) = %q, want web", got)
	}
	longID := strings.Repeat("a", 64)
	if got := containerName(container.Summary{Names: []string{"/"}, ID: longID}); got != longID[:12] {
		t.Fatalf("containerName(long id) = %q, want %q", got, longID[:12])
	}
	if got := containerName(container.Summary{ID: "short"}); got != "short" {
		t.Fatalf("containerName(short id) = %q, want short", got)
	}
	if got := containerName(container.Summary{}); got != "unknown" {
		t.Fatalf("containerName(empty) = %q, want unknown", got)
	}
}

func TestContainerNetworksSkipsNilEndpointSettings(t *testing.T) {
	if got := containerNetworks(container.Summary{}); got != nil {
		t.Fatalf("containerNetworks(empty) = %#v, want nil", got)
	}
	networks := containerNetworks(container.Summary{NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{
		"backend": &network.EndpointSettings{IPAddress: netip.MustParseAddr("10.0.0.2")},
		"broken":  nil,
	}}})
	if len(networks) != 1 || networks["backend"] != "10.0.0.2" {
		t.Fatalf("unexpected networks: %#v", networks)
	}
}

func TestShouldTriggerDockerEventFiltersContainerLifecycle(t *testing.T) {
	cases := []struct {
		name string
		msg  events.Message
		want bool
	}{
		{name: "start", msg: events.Message{Type: "container", Action: "start"}, want: true},
		{name: "connect", msg: events.Message{Type: "container", Action: "connect"}, want: true},
		{name: "image ignored", msg: events.Message{Type: "image", Action: "start"}, want: false},
		{name: "empty action ignored", msg: events.Message{Type: "container"}, want: false},
		{name: "exec ignored", msg: events.Message{Type: "container", Action: "exec_create"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldTriggerDockerEvent(tc.msg); got != tc.want {
				t.Fatalf("shouldTriggerDockerEvent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContainerNetworksPreservesIPv6OnlyEndpoints(t *testing.T) {
	networks := containerNetworks(container.Summary{NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{
		"v6":   {GlobalIPv6Address: netip.MustParseAddr("2001:db8::1")},
		"dual": {IPAddress: netip.MustParseAddr("192.0.2.1"), GlobalIPv6Address: netip.MustParseAddr("2001:db8::2")},
	}}})
	if networks["v6"] != "2001:db8::1" || networks["dual"] != "192.0.2.1" {
		t.Fatalf("unexpected network selection: %#v", networks)
	}
}
