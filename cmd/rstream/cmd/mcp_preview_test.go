// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

func TestMCPPreviewArgsBuildsPublishedHTTPTunnel(t *testing.T) {
	args := map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`), "name": json.RawMessage(`"codex-preview"`), "port": json.RawMessage(`"3000"`), "stable_domain": json.RawMessage(`"preview.example.com"`), "token_auth": json.RawMessage(`true`)}
	host, port, props, tokenAuth, err := mcpPreviewArgs(args, "project.cluster.example.com:443")
	if err != nil {
		t.Fatalf("mcpPreviewArgs returned error: %v", err)
	}
	if host != "127.0.0.1" || port != "3000" || !tokenAuth {
		t.Fatalf("unexpected preview args: host=%q port=%q tokenAuth=%t", host, port, tokenAuth)
	}
	if props.Name == nil || *props.Name != "codex-preview" {
		t.Fatalf("unexpected name: %#v", props.Name)
	}
	if props.Hostname == nil || *props.Hostname != "preview.example.com" {
		t.Fatalf("unexpected hostname: %#v", props.Hostname)
	}
	if props.Publish == nil || !*props.Publish || props.Protocol == nil || *props.Protocol != rstream.ProtocolHTTP || props.HTTPVersion == nil || *props.HTTPVersion != rstream.HTTP1_1 {
		t.Fatalf("unexpected tunnel properties: %#v", props)
	}
	if props.TokenAuth == nil || !*props.TokenAuth {
		t.Fatalf("token auth was not enabled")
	}
	if props.Labels["application-protocol"] != "rstream.preview" || props.Labels["rstream.preview.kind"] != "codex" || props.Labels["rstream.preview.port"] != "3000" {
		t.Fatalf("unexpected labels: %#v", props.Labels)
	}
}

func TestMCPPreviewForwardArgs(t *testing.T) {
	props := mcpPreviewTunnelProperties("codex-preview", "preview.example.com", "3000", true)
	args := strings.Join(mcpPreviewForwardArgs("127.0.0.1", "3000", props), "\n")
	for _, want := range []string{"forward", "127.0.0.1:3000", "--output\njson", "--name\ncodex-preview", "--publish", "--http", "--http-version\nhttp/1.1", "--host\npreview.example.com", "--token-auth", "--label\napplication-protocol=rstream.preview", "--label\nrstream.preview.kind=codex", "--label\nrstream.preview.port=3000"} {
		if !strings.Contains(args, want) {
			t.Fatalf("forward args do not contain %q: %s", want, args)
		}
	}
}

func TestMCPPreviewArgsRejectsInvalidPort(t *testing.T) {
	_, _, _, _, err := mcpPreviewArgs(map[string]json.RawMessage{"port": json.RawMessage(`"not-a-port"`)}, "project.cluster.example.com:443")
	if err == nil || !strings.Contains(err.Error(), "port must be numeric") {
		t.Fatalf("expected numeric port error, got %v", err)
	}
}

func TestMCPPreviewRegistryListPrunesDeadSessions(t *testing.T) {
	path := t.TempDir() + "/mcp-previews.json"
	session := mcpPreviewSession{ID: "preview-1", TunnelID: "tunnel-1", URL: "https://preview.example", CreatedAt: time.Now().UTC()}
	if err := writeMCPPreviewRegistry(path, []mcpPreviewSession{session}); err != nil {
		t.Fatalf("writeMCPPreviewRegistry returned error: %v", err)
	}
	sessions, err := mcpPreviewListFromPath(path)
	if err != nil {
		t.Fatalf("mcpPreviewListFromPath returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected dead preview to be pruned, got %#v", sessions)
	}
}

func TestMCPPreviewStopRemovesKnownDeadSession(t *testing.T) {
	path := t.TempDir() + "/mcp-previews.json"
	session := mcpPreviewSession{ID: "preview-1", TunnelID: "tunnel-1", URL: "https://preview.example", CreatedAt: time.Now().UTC()}
	if err := writeMCPPreviewRegistry(path, []mcpPreviewSession{session}); err != nil {
		t.Fatalf("writeMCPPreviewRegistry returned error: %v", err)
	}
	result, err := mcpPreviewStopFromPath(path, "tunnel-1")
	if err != nil {
		t.Fatalf("mcpPreviewStopFromPath returned error: %v", err)
	}
	if result["stopped"].(bool) {
		t.Fatalf("expected stopped=false for dead process, got %#v", result)
	}
	sessions, err := readMCPPreviewRegistry(path)
	if err != nil {
		t.Fatalf("readMCPPreviewRegistry returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected preview to be removed, got %#v", sessions)
	}
}
