// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

func TestMCPPublishTunnelProperties(t *testing.T) {
	cmd := mcpPublishTestCommand()
	if err := cmd.Flags().Set("name", "codex-mcp"); err != nil {
		t.Fatalf("failed to set name: %v", err)
	}
	if err := cmd.Flags().Set("label", "application-protocol=custom"); err != nil {
		t.Fatalf("failed to set label: %v", err)
	}
	props, err := newMCPPublishTunnelProperties(cmd)
	if err != nil {
		t.Fatalf("newMCPPublishTunnelProperties returned error: %v", err)
	}
	if props.Name == nil || *props.Name != "codex-mcp" {
		t.Fatalf("unexpected tunnel name: %#v", props.Name)
	}
	if props.Publish == nil || !*props.Publish {
		t.Fatalf("expected published MCP tunnel")
	}
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolHTTP {
		t.Fatalf("expected HTTP protocol: %#v", props.Protocol)
	}
	if props.TokenAuth == nil || !*props.TokenAuth {
		t.Fatalf("expected token auth on published MCP tunnel")
	}
	if props.Labels[mcpApplicationProtocolKey] != mcpApplicationProtocol || props.Labels[mcpTransportLabel] != mcpTransportStreamable || props.Labels[mcpPathLabel] != mcpHTTPPath {
		t.Fatalf("unexpected labels: %#v", props.Labels)
	}
}

func TestMCPPublishPrivateTunnelProperties(t *testing.T) {
	cmd := mcpPublishTestCommand()
	if err := cmd.Flags().Set("no-publish", "true"); err != nil {
		t.Fatalf("failed to set no-publish: %v", err)
	}
	props, err := newMCPPublishTunnelProperties(cmd)
	if err != nil {
		t.Fatalf("newMCPPublishTunnelProperties returned error: %v", err)
	}
	if props.Publish == nil || *props.Publish {
		t.Fatalf("expected private MCP tunnel")
	}
	if props.Protocol != nil || props.HTTPVersion != nil || props.TokenAuth != nil {
		t.Fatalf("private MCP tunnel should not force HTTP edge settings: %#v", props)
	}
}

func TestMCPHTTPHandlerToolsList(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, mcpHTTPPath, strings.NewReader(body))
	rec := httptest.NewRecorder()
	newMCPHTTPHandler(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("MCP-Protocol-Version") != mcpProtocolVersion {
		t.Fatalf("missing MCP protocol header")
	}
	if !strings.Contains(rec.Body.String(), "rstream_webtty_exec") {
		t.Fatalf("tools/list response missing WebTTY exec tool: %s", rec.Body.String())
	}
}

func TestMCPHTTPHandlerRejectsInvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, mcpHTTPPath, strings.NewReader("{"))
	rec := httptest.NewRecorder()
	newMCPHTTPHandler(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":-32700`) {
		t.Fatalf("expected JSON-RPC parse error: %s", rec.Body.String())
	}
}

func TestMCPPublishStatusOutput(t *testing.T) {
	cmd := mcpPublishTestCommand()
	output := bytes.Buffer{}
	cmd.SetOut(&output)
	status := mcpPublishStatus{Forwarding: "https://mcp.example", Path: mcpHTTPPath, Published: true, TokenAuth: true, URL: "https://mcp.example/mcp"}
	if err := printMCPPublishStatus(cmd, status); err != nil {
		t.Fatalf("printMCPPublishStatus returned error: %v", err)
	}
	if !strings.Contains(output.String(), "https://mcp.example/mcp") || !strings.Contains(output.String(), "Authorization: Bearer") {
		t.Fatalf("unexpected text output: %q", output.String())
	}
	output.Reset()
	status.Published = false
	if err := printMCPPublishStatus(cmd, status); err != nil {
		t.Fatalf("printMCPPublishStatus returned error: %v", err)
	}
	if strings.Contains(output.String(), "published") || !strings.Contains(output.String(), "available") {
		t.Fatalf("unexpected private text output: %q", output.String())
	}
	status.Published = true
	output.Reset()
	if err := cmd.Flags().Set("output", "json"); err != nil {
		t.Fatalf("failed to set output: %v", err)
	}
	cmd.SetOut(&output)
	if err := printMCPPublishStatus(cmd, status); err != nil {
		t.Fatalf("printMCPPublishStatus returned error: %v", err)
	}
	var decoded mcpPublishStatus
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid json output: %v\n%s", err, output.String())
	}
	if decoded.URL != status.URL {
		t.Fatalf("json output url = %q", decoded.URL)
	}
	if !decoded.TokenAuth {
		t.Fatalf("json output token_auth = false")
	}
}

func TestMCPPublishEndpointURLRemovesPrivateDisplaySuffix(t *testing.T) {
	if got := mcpPublishEndpointURL("rstrm://codex-mcp (unpublished)"); got != "rstrm://codex-mcp/mcp" {
		t.Fatalf("private endpoint URL = %q", got)
	}
	if got := mcpPublishEndpointURL("https://codex.example/"); got != "https://codex.example/mcp" {
		t.Fatalf("published endpoint URL = %q", got)
	}
}

func mcpPublishTestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "publish"}
	cmd.Flags().String("name", "rstream-mcp", "")
	cmd.Flags().Bool("publish", true, "")
	cmd.Flags().Bool("no-publish", false, "")
	cmd.Flags().String("host", "", "")
	cmd.Flags().StringArray("label", nil, "")
	cmd.Flags().String("output", "text", "")
	return cmd
}
