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
						webTTYCapabilitiesLabel:            WebTTYCapabilityExec + "," + WebTTYCapabilityFS,
						webTTYExecPathLabel:                WebTTYDefaultExecPath,
						webTTYFSPathLabel:                  WebTTYDefaultFSPath,
						webTTYFSModeLabel:                  WebTTYFSModeReadWrite,
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
		if server.Target != name {
			t.Fatalf("unexpected target: got %q want %q", server.Target, name)
		}
		if server.RstreamURL != "rstrm://"+name {
			t.Fatalf("unexpected rstream url: got %q want %q", server.RstreamURL, "rstrm://"+name)
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
		if len(server.Capabilities) != 2 || server.Capabilities[0] != WebTTYCapabilityExec || server.Capabilities[1] != WebTTYCapabilityFS {
			t.Fatalf("unexpected capabilities: %#v", server.Capabilities)
		}
		if server.ExecPath == nil || *server.ExecPath != WebTTYDefaultExecPath {
			t.Fatalf("unexpected exec path: %#v", server.ExecPath)
		}
		if server.FSPath == nil || *server.FSPath != WebTTYDefaultFSPath {
			t.Fatalf("unexpected fs path: %#v", server.FSPath)
		}
		if server.FSMode == nil || *server.FSMode != WebTTYFSModeReadWrite {
			t.Fatalf("unexpected fs mode: %#v", server.FSMode)
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
	t.Run("managed published server", func(t *testing.T) {
		name := "managed-prod-01"
		id := "managed-tunnel-1"
		serverID := "webtty-server-1"
		hostKeyID := "sha256:host-key"
		e2e := WebTTYE2ERequired
		clientProof := WebTTYClientProofRequired
		encryptionPolicy := "workspace_managed"
		publish := true
		protocol := rstream.ProtocolWebTTY
		host := "managed.example.net"
		tokenAuth := true
		list := ParseServers([]rstream.TunnelInventory{
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:        &id,
					Name:      &name,
					Publish:   &publish,
					Protocol:  &protocol,
					Hostname:  &host,
					TokenAuth: &tokenAuth,
					Labels: map[string]string{
						webTTYServerIDLabel:         serverID,
						webTTYHostKeyIDLabel:        hostKeyID,
						webTTYE2ELabel:              e2e,
						webTTYClientProofLabel:      clientProof,
						webTTYEncryptionPolicyLabel: encryptionPolicy,
					},
				},
				Status: "online",
			},
		})
		if len(list) != 1 {
			t.Fatalf("expected one managed server, got %d", len(list))
		}
		server := list[0]
		if !server.Managed {
			t.Fatalf("expected managed server")
		}
		if server.TunnelProtocol != string(rstream.ProtocolWebTTY) {
			t.Fatalf("unexpected tunnel protocol: got %q want %q", server.TunnelProtocol, rstream.ProtocolWebTTY)
		}
		if server.ServerID == nil || *server.ServerID != serverID {
			t.Fatalf("unexpected server id: %#v", server.ServerID)
		}
		if server.HostKeyID == nil || *server.HostKeyID != hostKeyID {
			t.Fatalf("unexpected host key id: %#v", server.HostKeyID)
		}
		if server.E2E == nil || *server.E2E != e2e {
			t.Fatalf("unexpected E2E label: %#v", server.E2E)
		}
		if server.ClientProof == nil || *server.ClientProof != clientProof {
			t.Fatalf("unexpected client proof label: %#v", server.ClientProof)
		}
		if server.EncryptionPolicy == nil || *server.EncryptionPolicy != encryptionPolicy {
			t.Fatalf("unexpected encryption policy: %#v", server.EncryptionPolicy)
		}
		if server.Target != name {
			t.Fatalf("unexpected managed target: got %q want %q", server.Target, name)
		}
		if server.RstreamURL != "rstrm://"+serverID {
			t.Fatalf("unexpected managed rstream url: got %q want %q", server.RstreamURL, "rstrm://"+serverID)
		}
		if server.Host == nil || *server.Host != host {
			t.Fatalf("unexpected host: %#v", server.Host)
		}
		if !server.TokenAuth {
			t.Fatalf("expected token auth to be enabled")
		}
		if server.ExecPath == nil || *server.ExecPath != WebTTYDefaultExecPath {
			t.Fatalf("expected default exec path, got %#v", server.ExecPath)
		}
	})
	t.Run("managed private server", func(t *testing.T) {
		name := "managed-private-01"
		id := "managed-private-tunnel-1"
		serverID := "webtty-server-private"
		publish := false
		protocol := rstream.ProtocolWebTTY
		list := ParseServers([]rstream.TunnelInventory{
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:       &id,
					Name:     &name,
					Publish:  &publish,
					Protocol: &protocol,
					Labels: map[string]string{
						webTTYServerIDLabel: serverID,
					},
				},
				Status: "online",
			},
		})
		if len(list) != 1 {
			t.Fatalf("expected one managed private server, got %d", len(list))
		}
		server := list[0]
		if !server.Managed || server.Publish {
			t.Fatalf("unexpected managed private flags: %#v", server)
		}
		if server.TunnelProtocol != string(rstream.ProtocolWebTTY) {
			t.Fatalf("unexpected tunnel protocol: got %q want %q", server.TunnelProtocol, rstream.ProtocolWebTTY)
		}
		if server.ServerID == nil || *server.ServerID != serverID {
			t.Fatalf("unexpected server id: %#v", server.ServerID)
		}
		if server.Target != name || server.RstreamURL != "rstrm://"+serverID {
			t.Fatalf("unexpected private managed target: %#v", server)
		}
		if server.TokenAuth {
			t.Fatalf("private managed server should not report edge token auth")
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
		if len(server.Capabilities) != 1 || server.Capabilities[0] != WebTTYCapabilityExec {
			t.Fatalf("expected default exec capability, got %#v", server.Capabilities)
		}
		if server.ExecPath == nil || *server.ExecPath != WebTTYDefaultExecPath {
			t.Fatalf("expected default exec path, got %#v", server.ExecPath)
		}
		if server.Publish {
			t.Fatalf("expected private server")
		}
	})
	t.Run("registered server name is display target and server id is dial target", func(t *testing.T) {
		tunnelName := "registered-server-id"
		id := "registered-tunnel-1"
		serverName := "production-shell"
		publish := false
		protocol := rstream.ProtocolWebTTY
		list := ParseServers([]rstream.TunnelInventory{
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:       &id,
					Name:     &tunnelName,
					Publish:  &publish,
					Protocol: &protocol,
					Labels: map[string]string{
						webTTYServerIDLabel:   tunnelName,
						webTTYServerNameLabel: serverName,
					},
				},
				Status: "online",
			},
		})
		if len(list) != 1 {
			t.Fatalf("expected one registered server, got %d", len(list))
		}
		server := list[0]
		if server.Target != serverName {
			t.Fatalf("target = %q, want %q", server.Target, serverName)
		}
		if server.RstreamURL != "rstrm://"+tunnelName {
			t.Fatalf("rstream url = %q, want %q", server.RstreamURL, "rstrm://"+tunnelName)
		}
		if server.ServerName == nil || *server.ServerName != serverName {
			t.Fatalf("server name = %#v", server.ServerName)
		}
	})
	t.Run("security labels are explicit per server", func(t *testing.T) {
		publish := true
		protocol := rstream.ProtocolWebTTY
		tests := []struct {
			name            string
			labels          map[string]string
			wantE2E         string
			wantClientProof string
			wantEncryption  string
			wantHostKeyID   string
		}{
			{
				name: "disabled",
				labels: map[string]string{
					webTTYServerIDLabel:         "plain-server",
					webTTYE2ELabel:              WebTTYE2EDisabled,
					webTTYClientProofLabel:      WebTTYClientProofNone,
					webTTYEncryptionPolicyLabel: "disabled",
				},
				wantE2E:         WebTTYE2EDisabled,
				wantClientProof: WebTTYClientProofNone,
				wantEncryption:  "disabled",
			},
			{
				name: "explicit key",
				labels: map[string]string{
					webTTYServerIDLabel:         "explicit-server",
					webTTYHostKeyIDLabel:        "sha256:explicit",
					webTTYE2ELabel:              WebTTYE2ERequired,
					webTTYClientProofLabel:      WebTTYClientProofRequired,
					webTTYEncryptionPolicyLabel: "explicit_key",
				},
				wantE2E:         WebTTYE2ERequired,
				wantClientProof: WebTTYClientProofRequired,
				wantEncryption:  "explicit_key",
				wantHostKeyID:   "sha256:explicit",
			},
			{
				name: "workspace managed",
				labels: map[string]string{
					webTTYServerIDLabel:         "workspace-server",
					webTTYHostKeyIDLabel:        "sha256:workspace",
					webTTYE2ELabel:              WebTTYE2ERequired,
					webTTYClientProofLabel:      WebTTYClientProofRequired,
					webTTYEncryptionPolicyLabel: "workspace_managed",
				},
				wantE2E:         WebTTYE2ERequired,
				wantClientProof: WebTTYClientProofRequired,
				wantEncryption:  "workspace_managed",
				wantHostKeyID:   "sha256:workspace",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				id := "tunnel-" + tt.name
				list := ParseServers([]rstream.TunnelInventory{{
					TunnelProperties: rstream.TunnelProperties{
						ID:       &id,
						Name:     &tt.name,
						Publish:  &publish,
						Protocol: &protocol,
						Labels:   tt.labels,
					},
					Status: "online",
				}})
				if len(list) != 1 {
					t.Fatalf("expected one server, got %d", len(list))
				}
				server := list[0]
				if server.E2E == nil || *server.E2E != tt.wantE2E {
					t.Fatalf("E2E = %#v, want %q", server.E2E, tt.wantE2E)
				}
				if server.ClientProof == nil || *server.ClientProof != tt.wantClientProof {
					t.Fatalf("client proof = %#v, want %q", server.ClientProof, tt.wantClientProof)
				}
				if server.EncryptionPolicy == nil || *server.EncryptionPolicy != tt.wantEncryption {
					t.Fatalf("encryption policy = %#v, want %q", server.EncryptionPolicy, tt.wantEncryption)
				}
				if tt.wantHostKeyID == "" {
					if server.HostKeyID != nil {
						t.Fatalf("host key id = %#v, want nil", server.HostKeyID)
					}
				} else if server.HostKeyID == nil || *server.HostKeyID != tt.wantHostKeyID {
					t.Fatalf("host key id = %#v, want %q", server.HostKeyID, tt.wantHostKeyID)
				}
			})
		}
	})
	t.Run("capabilities keep a canonical known set", func(t *testing.T) {
		id := "tunnel-capabilities"
		publish := false
		list := ParseServers([]rstream.TunnelInventory{
			{
				TunnelProperties: rstream.TunnelProperties{
					ID:      &id,
					Publish: &publish,
					Labels: map[string]string{
						webTTYApplicationProtocolKey: WebTTYApplicationProtocol,
						webTTYCapabilitiesLabel:      WebTTYCapabilityFS + ",unknown," + WebTTYCapabilityExec + "," + WebTTYCapabilityFS,
					},
				},
				Status: "online",
			},
		})
		if len(list) != 1 {
			t.Fatalf("expected one server, got %d", len(list))
		}
		if len(list[0].Capabilities) != 2 || list[0].Capabilities[0] != WebTTYCapabilityExec || list[0].Capabilities[1] != WebTTYCapabilityFS {
			t.Fatalf("unexpected capabilities: %#v", list[0].Capabilities)
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
