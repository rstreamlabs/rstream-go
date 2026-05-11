// See LICENSE file in the project root for license information.

package webtty

import (
	"testing"

	rstream "github.com/rstreamlabs/rstream-go"
)

func TestParseServers(t *testing.T) {
	t.Run("published server", func(t *testing.T) {
		name := "shell-prod-01"
		id := "tunnel-1"
		publish := true
		protocol := rstream.ProtocolHTTP
		host := "shell.example.net"
		tokenAuth := true
		list := ParseServers([]rstream.TunnelInventory{
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:        &id,
					Name:      &name,
					Publish:   &publish,
					Protocol:  &protocol,
					Host:      &host,
					TokenAuth: &tokenAuth,
					Labels: map[string]string{
						webTTYApplicationProtocolKey:       WebTTYApplicationProtocol,
						webTTYHostnameLabel:                "prod-01",
						webTTYOSPrettyNameLabel:            "Ubuntu 24.04.2 LTS",
						webTTYOSFamilyLabel:                "linux",
						webTTYArchLabel:                    "amd64",
						webTTYLabelPrefix + "env":          "prod",
						webTTYLabelPrefix + "service":      "shell",
						webTTYOSVersionCodenameLabel:       "noble",
						webTTYOSVersionIDLabel:             "24.04",
						webTTYKernelReleaseLabel:           "6.8.0",
						webTTYOSIDLabel:                    "ubuntu",
						webTTYLabelPrefix + "empty-value":  "",
						webTTYLabelPrefix + " spaced-key ": "value",
					},
				},
				Status: "online",
			},
		})
		if len(list) != 1 {
			t.Fatalf("expected one server, got %d", len(list))
		}
		server := list[0]
		if server.TunnelID != id {
			t.Fatalf("unexpected tunnel id: got %q want %q", server.TunnelID, id)
		}
		if server.TunnelName == nil || *server.TunnelName != name {
			t.Fatalf("unexpected tunnel name: %#v", server.TunnelName)
		}
		if server.Target != id {
			t.Fatalf("unexpected target: got %q want %q", server.Target, id)
		}
		if server.RstreamURL != "rstrm://"+id {
			t.Fatalf("unexpected rstream url: got %q want %q", server.RstreamURL, "rstrm://"+id)
		}
		if !server.Publish {
			t.Fatalf("expected published server")
		}
		if server.Host == nil || *server.Host != host {
			t.Fatalf("unexpected host: %#v", server.Host)
		}
		if !server.TokenAuth {
			t.Fatalf("expected token auth to be enabled")
		}
		if server.Hostname == nil || *server.Hostname != "prod-01" {
			t.Fatalf("unexpected hostname: %#v", server.Hostname)
		}
		if server.OSPrettyName == nil || *server.OSPrettyName != "Ubuntu 24.04.2 LTS" {
			t.Fatalf("unexpected os pretty name: %#v", server.OSPrettyName)
		}
		if got := server.Labels["env"]; got != "prod" {
			t.Fatalf("unexpected custom env label: got %q want %q", got, "prod")
		}
		if got := server.Labels["service"]; got != "shell" {
			t.Fatalf("unexpected custom service label: got %q want %q", got, "shell")
		}
		if got := server.Labels["spaced-key"]; got != "value" {
			t.Fatalf("unexpected trimmed custom label key: got %q want %q", got, "value")
		}
		if got := server.Labels["empty-value"]; got != "" {
			t.Fatalf("unexpected empty custom label value: got %q want empty string", got)
		}
	})
	t.Run("private server uses tunnel id when name is missing", func(t *testing.T) {
		id := "tunnel-2"
		publish := false
		list := ParseServers([]rstream.TunnelInventory{
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:      &id,
					Publish: &publish,
					Labels: map[string]string{
						webTTYApplicationProtocolKey: WebTTYApplicationProtocol,
					},
				},
				Status: "online",
			},
		})
		if len(list) != 1 {
			t.Fatalf("expected one server, got %d", len(list))
		}
		server := list[0]
		if server.Target != id {
			t.Fatalf("unexpected target: got %q want %q", server.Target, id)
		}
		if server.RstreamURL != "rstrm://"+id {
			t.Fatalf("unexpected rstream url: got %q want %q", server.RstreamURL, "rstrm://"+id)
		}
		if server.TunnelName != nil {
			t.Fatalf("expected tunnel name to be absent, got %#v", server.TunnelName)
		}
		if server.Publish {
			t.Fatalf("expected private server")
		}
	})
	t.Run("skip non webtty or invalid published tunnel", func(t *testing.T) {
		id1 := "tunnel-3"
		id2 := "tunnel-4"
		publish := true
		protocol := rstream.ProtocolTLS
		list := ParseServers([]rstream.TunnelInventory{
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:      &id1,
					Publish: &publish,
					Labels: map[string]string{
						webTTYApplicationProtocolKey: "other.protocol",
					},
				},
			},
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:       &id2,
					Publish:  &publish,
					Protocol: &protocol,
					Labels: map[string]string{
						webTTYApplicationProtocolKey: WebTTYApplicationProtocol,
					},
				},
			},
		})
		if len(list) != 0 {
			t.Fatalf("expected empty server list, got %#v", list)
		}
	})
}
