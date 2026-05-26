// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
)

func TestMCPRemoteExposeArgsBuildsPublishedMCPForward(t *testing.T) {
	args := map[string]json.RawMessage{"webtty_url": json.RawMessage(`"rstrm://robot-shell"`), "exec_path": json.RawMessage(`"/exec"`), "port": json.RawMessage(`"8765"`), "id": json.RawMessage(`"robot mcp"`), "name": json.RawMessage(`"robot-mcp"`), "mcp_path": json.RawMessage(`"/mcp"`), "stable_domain": json.RawMessage(`"robot.example.com"`), "labels": json.RawMessage(`["fleet=lab","role=robot"]`), "env": json.RawMessage(`["RSTREAM_CONTEXT=prod"]`), "workdir": json.RawMessage(`"/srv/robot"`), "user": json.RawMessage(`"robot"`)}
	expose, err := mcpRemoteExposeArgs(args)
	if err != nil {
		t.Fatalf("mcpRemoteExposeArgs returned error: %v", err)
	}
	if expose.ID != "robot-mcp" || expose.Name != "robot-mcp" || expose.ExecPath != "/exec" || !expose.Publish || !expose.TokenAuth || expose.Workdir == nil || *expose.Workdir != "/srv/robot" || expose.User == nil || *expose.User != "robot" {
		t.Fatalf("unexpected expose args: %#v", expose)
	}
	if expose.Labels[mcpApplicationProtocolKey] != mcpApplicationProtocol || expose.Labels[remoteExposeKindLabel] != "mcp" || expose.Labels[remoteExposePortLabel] != "8765" || expose.Labels["fleet"] != "lab" {
		t.Fatalf("unexpected labels: %#v", expose.Labels)
	}
	forwardArgs, err := remoteExposeForwardArgs(expose)
	if err != nil {
		t.Fatalf("remoteExposeForwardArgs returned error: %v", err)
	}
	joined := strings.Join(forwardArgs, "\n")
	for _, want := range []string{"--publish", "--http", "--http-version", "http/1.1", "--host\nrobot.example.com", "--token-auth", "--label\napplication-protocol=rstream.mcp", "--label\nrstream.remote.kind=mcp"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("forward args missing %q: %#v", want, forwardArgs)
		}
	}
}

func TestMCPRemoteExposeSupportsPrivateDatagram(t *testing.T) {
	expose := remoteExposeArgs{Host: "127.0.0.1", Name: "robot-udp", Port: "9999", Protocol: "udp", Publish: false, RstreamCmd: "rstream"}
	args, err := remoteExposeForwardArgs(expose)
	if err != nil {
		t.Fatalf("remoteExposeForwardArgs returned error: %v", err)
	}
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--no-publish") || !strings.Contains(joined, "--datagram") {
		t.Fatalf("unexpected datagram args: %#v", args)
	}
	privateH3Args, err := remoteExposeForwardProtocolArgs("h3", false)
	if err != nil || strings.Join(privateH3Args, "\n") != "--datagram" {
		t.Fatalf("private h3 args = %#v, %v", privateH3Args, err)
	}
	if _, err := remoteExposeProtocolArgs("smtp"); err == nil {
		t.Fatalf("expected invalid protocol error")
	}
}

func TestMCPRemoteExposePrivateMCPDoesNotUsePublicAuthOptions(t *testing.T) {
	expose, err := mcpRemoteExposeArgs(map[string]json.RawMessage{"webtty_url": json.RawMessage(`"rstrm://robot"`), "port": json.RawMessage(`"8765"`), "publish": json.RawMessage(`false`), "mcp_path": json.RawMessage(`"/mcp"`), "token_auth": json.RawMessage(`true`)})
	if err != nil {
		t.Fatalf("mcpRemoteExposeArgs returned error: %v", err)
	}
	args, err := remoteExposeForwardArgs(expose)
	if err != nil {
		t.Fatalf("remoteExposeForwardArgs returned error: %v", err)
	}
	joined := strings.Join(args, "\n")
	if expose.TokenAuth || strings.Contains(joined, "--token-auth") || strings.Contains(joined, "--http") || !strings.Contains(joined, "--no-publish") || !strings.Contains(joined, "--bytestream") {
		t.Fatalf("unexpected private MCP args: expose=%#v args=%#v", expose, args)
	}
	if _, err := mcpRemoteExposeArgs(map[string]json.RawMessage{"webtty_url": json.RawMessage(`"rstrm://robot"`), "port": json.RawMessage(`"8765"`), "publish": json.RawMessage(`false`), "stable_domain": json.RawMessage(`"robot.example.com"`)}); err == nil {
		t.Fatalf("expected private stable domain error")
	}
}

func TestRemoteExposeShellScriptUsesSafeIDAndQuotedCommand(t *testing.T) {
	expose := remoteExposeArgs{ID: "robot rm -rf", Host: "127.0.0.1", Name: "robot mcp", Port: "8765", Protocol: "http", Publish: true, RstreamCmd: "/tmp/rstream cli", Timeout: 1}
	script := remoteExposeShellScript(expose)
	if !strings.Contains(script, "id='robot-rm--rf'") || !strings.Contains(script, "'/tmp/rstream cli'") || !strings.Contains(script, "printf '%s%s\\n' 'RSTREAM_REMOTE_EXPOSE_JSON_LINE=' \"$line\"") {
		t.Fatalf("unexpected shell script: %s", script)
	}
}

func TestParseRemoteExposeResult(t *testing.T) {
	stdout := "RSTREAM_REMOTE_EXPOSE_STATUS=online\nRSTREAM_REMOTE_EXPOSE_ID=robot\nRSTREAM_REMOTE_EXPOSE_PID=42\nRSTREAM_REMOTE_EXPOSE_LOG=/tmp/robot.log\nRSTREAM_REMOTE_EXPOSE_JSON_LINE={\"status\":\"online\",\"tunnel_id\":\"abc\",\"forwarding\":\"https://robot.example.com\"}\n"
	parsed, err := parseRemoteExposeResult(remoteExposeArgs{ID: "robot"}, []string{"rstream"}, &webTTYClientResult{ExitCode: 0, Stdout: stdout})
	if err != nil {
		t.Fatalf("parseRemoteExposeResult returned error: %v", err)
	}
	if parsed.Status != "online" || parsed.PID != 42 || parsed.LogPath != "/tmp/robot.log" || parsed.Forward == nil || parsed.Forward.TunnelID == nil || *parsed.Forward.TunnelID != "abc" {
		t.Fatalf("unexpected parsed result: %#v", parsed)
	}
}

func TestRemoteMCPListParams(t *testing.T) {
	params, err := remoteMCPListParams("labels.fleet=lab")
	if err != nil {
		t.Fatalf("remoteMCPListParams returned error: %v", err)
	}
	if params.Filters == nil || params.Filters.Status == nil || *params.Filters.Status != "online" || params.Filters.Labels[mcpApplicationProtocolKey] == nil || *params.Filters.Labels[mcpApplicationProtocolKey] != mcpApplicationProtocol || params.Filters.Labels["fleet"] == nil || *params.Filters.Labels["fleet"] != "lab" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestRemoteMCPEndpointPayload(t *testing.T) {
	tunnel := rstream.TunnelInventory{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("abc"), Name: rstream.StringPtr("robot-mcp"), Hostname: rstream.StringPtr("robot.example.com"), Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP), Labels: map[string]string{mcpPathLabel: "/robot/mcp"}, TokenAuth: rstream.BoolPtr(true)}, Status: "online"}
	payload := remoteMCPEndpointPayload(tunnel)
	if payload["rstrm_url"] != "rstrm://robot-mcp" || payload["url"] != "https://robot.example.com/robot/mcp" || payload["path"] != "/robot/mcp" || payload["token_auth"] != true {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestResolveRemoteMCPHTTPEndpointAppendsPath(t *testing.T) {
	endpoint, err := resolveRemoteMCPEndpoint("https://robot.example.com", map[string]json.RawMessage{"path": json.RawMessage(`"/mcp"`)})
	if err != nil {
		t.Fatalf("resolveRemoteMCPEndpoint returned error: %v", err)
	}
	if endpoint.URL != "https://robot.example.com/mcp" {
		t.Fatalf("unexpected endpoint URL: %q", endpoint.URL)
	}
}

func TestRemoteMCPArguments(t *testing.T) {
	values, err := remoteMCPArguments(map[string]json.RawMessage{"arguments": json.RawMessage(`{"path":"/tmp"}`)})
	if err != nil || values["path"] != "/tmp" {
		t.Fatalf("remoteMCPArguments(object) = %#v, %v", values, err)
	}
	values, err = remoteMCPArguments(map[string]json.RawMessage{"arguments_json": json.RawMessage(`"{\"command\":\"status\"}"`)})
	if err != nil || values["command"] != "status" {
		t.Fatalf("remoteMCPArguments(json) = %#v, %v", values, err)
	}
	if _, err := remoteMCPArguments(map[string]json.RawMessage{"arguments": json.RawMessage(`[]`)}); err == nil {
		t.Fatalf("expected array argument error")
	}
}

func TestRemoteMCPJSONRPCPostsBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer token-1" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: method=%s auth=%q content-type=%q", r.Method, r.Header.Get("Authorization"), r.Header.Get("Content-Type"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if payload["method"] != "tools/list" {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer server.Close()
	result, err := remoteMCPJSONRPC(context.Background(), remoteMCPEndpoint{Client: server.Client(), URL: server.URL}, "token-1", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if err != nil {
		t.Fatalf("remoteMCPJSONRPC returned error: %v", err)
	}
	if result["jsonrpc"] != "2.0" {
		t.Fatalf("unexpected response: %#v", result)
	}
}
