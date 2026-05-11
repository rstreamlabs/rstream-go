// See LICENSE file in the project root for license information.

package rundocker

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
)

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
