// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

func TestMCPLocalTunnelArgsBuildsPublishedHTTPTunnel(t *testing.T) {
	args := map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`), "name": json.RawMessage(`"codex-local-tunnel"`), "port": json.RawMessage(`"3000"`), "stable_domain": json.RawMessage(`"local-tunnel.example.com"`), "token_auth": json.RawMessage(`true`)}
	host, port, props, err := mcpLocalTunnelArgs(args, "project.cluster.example.com:443")
	if err != nil {
		t.Fatalf("mcpLocalTunnelArgs returned error: %v", err)
	}
	if host != "127.0.0.1" || port != "3000" {
		t.Fatalf("unexpected local tunnel args: host=%q port=%q", host, port)
	}
	if props.Name == nil || *props.Name != "codex-local-tunnel" {
		t.Fatalf("unexpected name: %#v", props.Name)
	}
	if props.Hostname == nil || *props.Hostname != "local-tunnel.example.com" {
		t.Fatalf("unexpected hostname: %#v", props.Hostname)
	}
	if props.Publish == nil || !*props.Publish || props.Protocol == nil || *props.Protocol != rstream.ProtocolHTTP || props.HTTPVersion == nil || *props.HTTPVersion != rstream.HTTP1_1 {
		t.Fatalf("unexpected tunnel properties: %#v", props)
	}
	if props.TokenAuth == nil || !*props.TokenAuth {
		t.Fatalf("token auth was not enabled")
	}
	if props.Labels["application-protocol"] != "rstream.local_tunnel" || props.Labels["rstream.local_tunnel.kind"] != "codex" || props.Labels["rstream.local_tunnel.port"] != "3000" {
		t.Fatalf("unexpected labels: %#v", props.Labels)
	}
}

func TestMCPLocalTunnelForwardArgs(t *testing.T) {
	props, err := mcpLocalTunnelProperties(map[string]json.RawMessage{"token_auth": json.RawMessage(`true`)}, "codex-local-tunnel", "local-tunnel.example.com", "3000", true)
	if err != nil {
		t.Fatalf("mcpLocalTunnelProperties returned error: %v", err)
	}
	args := strings.Join(mcpLocalTunnelForwardArgs("127.0.0.1", "3000", props), "\n")
	for _, want := range []string{"forward", "127.0.0.1:3000", "--output\njson", "--name\ncodex-local-tunnel", "--publish", "--http", "--http-version\nhttp/1.1", "--host\nlocal-tunnel.example.com", "--token-auth", "--label\napplication-protocol=rstream.local_tunnel", "--label\nrstream.local_tunnel.kind=codex", "--label\nrstream.local_tunnel.port=3000"} {
		if !strings.Contains(args, want) {
			t.Fatalf("forward args do not contain %q: %s", want, args)
		}
	}
}

func TestMCPLocalTunnelForwardArgsCoversAdvancedHTTPOptions(t *testing.T) {
	input := map[string]json.RawMessage{
		"challenge_mode": json.RawMessage(`true`),
		"geoip":          json.RawMessage(`["FR","US"]`),
		"http_version":   json.RawMessage(`"h2c"`),
		"labels":         json.RawMessage(`["env=test"]`),
		"mtls":           json.RawMessage(`true`),
		"rstream_auth":   json.RawMessage(`true`),
		"trusted_ips":    json.RawMessage(`["10.0.0.0/8"]`),
		"upstream_tls":   json.RawMessage(`true`),
	}
	props, err := mcpLocalTunnelProperties(input, "codex-local-tunnel", "", "3000", true)
	if err != nil {
		t.Fatalf("mcpLocalTunnelProperties returned error: %v", err)
	}
	args := strings.Join(mcpLocalTunnelForwardArgs("127.0.0.1", "3000", props), "\n")
	for _, want := range []string{"--http-version\nh2c", "--rstream-auth", "--challenge-mode", "--mtls", "--upstream-tls", "--geoip\nFR,US", "--trusted-ips\n10.0.0.0/8", "--label\nenv=test"} {
		if !strings.Contains(args, want) {
			t.Fatalf("forward args do not contain %q: %s", want, args)
		}
	}
}

func TestMCPLocalTunnelForwardArgsUsesPrivateRawTunnel(t *testing.T) {
	input := map[string]json.RawMessage{"protocol": json.RawMessage(`"h3"`)}
	props, err := mcpLocalTunnelProperties(input, "codex-local-tunnel", "", "3000", false)
	if err != nil {
		t.Fatalf("mcpLocalTunnelProperties returned error: %v", err)
	}
	args := strings.Join(mcpLocalTunnelForwardArgs("127.0.0.1", "3000", props), "\n")
	if !strings.Contains(args, "--no-publish") || !strings.Contains(args, "--datagram") || strings.Contains(args, "--http") {
		t.Fatalf("unexpected private forward args: %s", args)
	}
}

func TestMCPLocalTunnelArgsRejectsInvalidPort(t *testing.T) {
	_, _, _, err := mcpLocalTunnelArgs(map[string]json.RawMessage{"port": json.RawMessage(`"not-a-port"`)}, "project.cluster.example.com:443")
	if err == nil || !strings.Contains(err.Error(), "port must be numeric") {
		t.Fatalf("expected numeric port error, got %v", err)
	}
}

func TestMCPLocalTunnelArgsRejectsIncompatibleOptions(t *testing.T) {
	tests := []struct {
		name string
		args map[string]json.RawMessage
		want string
	}{
		{name: "rstream auth on tls", args: map[string]json.RawMessage{"port": json.RawMessage(`"3000"`), "protocol": json.RawMessage(`"tls"`), "rstream_auth": json.RawMessage(`true`)}, want: "HTTP options require protocol=http"},
		{name: "stable domain private", args: map[string]json.RawMessage{"port": json.RawMessage(`"3000"`), "publish": json.RawMessage(`false`), "stable_domain": json.RawMessage(`"local-tunnel.example.com"`)}, want: "stable_domain requires publish=true"},
		{name: "token auth on raw bytestream", args: map[string]json.RawMessage{"port": json.RawMessage(`"3000"`), "protocol": json.RawMessage(`"tcp"`), "token_auth": json.RawMessage(`true`)}, want: "HTTP options require protocol=http"},
		{name: "tls mode on http", args: map[string]json.RawMessage{"port": json.RawMessage(`"3000"`), "tls_mode": json.RawMessage(`"passthrough"`)}, want: "tls_mode requires protocol=tls"},
		{name: "h3 bytestream", args: map[string]json.RawMessage{"port": json.RawMessage(`"3000"`), "protocol": json.RawMessage(`"h3"`), "tunnel_type": json.RawMessage(`"bytestream"`)}, want: "http_version=h3 requires tunnel_type=datagram"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := mcpLocalTunnelArgs(tt.args, "project.cluster.example.com:443")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestMCPLocalTunnelRegistryListPrunesDeadSessions(t *testing.T) {
	path := t.TempDir() + "/mcp-local-tunnels.json"
	session := mcpLocalTunnelSession{ID: "local-tunnel-1", TunnelID: "tunnel-1", URL: "https://local-tunnel.example", CreatedAt: time.Now().UTC()}
	if err := writeMCPLocalTunnelRegistry(path, []mcpLocalTunnelSession{session}); err != nil {
		t.Fatalf("writeMCPLocalTunnelRegistry returned error: %v", err)
	}
	sessions, err := mcpLocalTunnelListFromPath(path)
	if err != nil {
		t.Fatalf("mcpLocalTunnelListFromPath returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected dead local tunnel to be pruned, got %#v", sessions)
	}
}

func TestMCPLocalTunnelStopRemovesKnownDeadSession(t *testing.T) {
	path := t.TempDir() + "/mcp-local-tunnels.json"
	session := mcpLocalTunnelSession{ID: "local-tunnel-1", TunnelID: "tunnel-1", URL: "https://local-tunnel.example", CreatedAt: time.Now().UTC()}
	if err := writeMCPLocalTunnelRegistry(path, []mcpLocalTunnelSession{session}); err != nil {
		t.Fatalf("writeMCPLocalTunnelRegistry returned error: %v", err)
	}
	result, err := mcpLocalTunnelStopFromPath(path, "tunnel-1")
	if err != nil {
		t.Fatalf("mcpLocalTunnelStopFromPath returned error: %v", err)
	}
	if result["stopped"].(bool) {
		t.Fatalf("expected stopped=false for dead process, got %#v", result)
	}
	sessions, err := readMCPLocalTunnelRegistry(path)
	if err != nil {
		t.Fatalf("readMCPLocalTunnelRegistry returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected local tunnel to be removed, got %#v", sessions)
	}
}
