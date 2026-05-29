// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func TestMCPReadWriteFraming(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	framed := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(input), input)
	message, err := readMCPMessage(bufio.NewReader(strings.NewReader(framed)))
	if err != nil {
		t.Fatalf("readMCPMessage returned error: %v", err)
	}
	if message.Method != "tools/list" || string(message.ID) != "1" {
		t.Fatalf("unexpected message: %#v", message)
	}
	var output bytes.Buffer
	if err := writeMCPResponse(&output, mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]string{"ok": "true"}}); err != nil {
		t.Fatalf("writeMCPResponse returned error: %v", err)
	}
	if !strings.HasPrefix(output.String(), "Content-Length: ") || !strings.Contains(output.String(), `"jsonrpc":"2.0"`) {
		t.Fatalf("unexpected framed response: %q", output.String())
	}
}

func TestMCPReadLineDelimitedJSON(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":0,"method":"initialize"}` + "\n"
	message, err := readMCPMessage(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("readMCPMessage returned error: %v", err)
	}
	if message.Method != "initialize" || string(message.ID) != "0" {
		t.Fatalf("unexpected message: %#v", message)
	}
	var output bytes.Buffer
	if err := writeMCPResponseWithFraming(&output, mcpResponse{JSONRPC: "2.0", ID: message.ID, Result: map[string]string{"ok": "true"}}, mcpFramingLineDelimited); err != nil {
		t.Fatalf("writeMCPResponseWithFraming returned error: %v", err)
	}
	if !strings.HasSuffix(output.String(), "\n") || strings.HasPrefix(output.String(), "Content-Length:") || !strings.Contains(output.String(), `"ok":"true"`) {
		t.Fatalf("unexpected line-delimited response: %q", output.String())
	}
}

func TestServeMCPReturnsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	defer inputWriter.Close()
	done := make(chan error, 1)
	go func() { done <- serveMCP(ctx, inputReader, &bytes.Buffer{}) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveMCP returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveMCP did not return after context cancellation")
	}
}

func TestMCPReadRejectsInvalidContentLength(t *testing.T) {
	oversized := fmt.Sprintf("Content-Length: %d\r\n\r\n{}", mcpMaxMessageBytes+1)
	if _, err := readMCPMessage(bufio.NewReader(strings.NewReader(oversized))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized Content-Length error = %v", err)
	}
	negative := "Content-Length: -1\r\n\r\n{}"
	if _, err := readMCPMessage(bufio.NewReader(strings.NewReader(negative))); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative Content-Length error = %v", err)
	}
}

func TestMCPToolsListContainsAgentNativeTools(t *testing.T) {
	response := handleMCPMessage(t.Context(), mcpMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/list"})
	if response.Error != nil {
		t.Fatalf("unexpected error: %#v", response.Error)
	}
	payload, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	for _, want := range []string{"rstream_auth_poll", "rstream_auth_start", "rstream_context_list", "rstream_context_get", "rstream_project_creation_options", "rstream_project_create", "rstream_project_delete", "rstream_project_list", "rstream_project_logs", "rstream_project_usage", "rstream_project_plan_get", "rstream_project_turn_usage", "rstream_project_turn_credentials_create", "rstream_project_domains_list", "rstream_project_domain_create", "rstream_project_domain_get", "rstream_project_domain_delete", "rstream_project_domain_verify", "rstream_project_domain_connect", "rstream_project_settings_get", "rstream_project_settings_patch", "rstream_project_settings_reset", "rstream_local_tunnel_expose", "rstream_local_tunnel_list", "rstream_local_tunnel_stop", "rstream_remote_expose", "rstream_remote_expose_stop", "rstream_remote_mcp_discover", "rstream_remote_mcp_tools", "rstream_remote_mcp_call", "rstream_runtime_prepare", "rstream_runtime_status", "rstream_token_create", "rstream_workspace_list", "rstream_workspace_members_list", "rstream_webtty_list", "rstream_webtty_exec", "rstream_webtty_fs_list", "rstream_webtty_fs_read", "rstream_webtty_fs_download", "rstream_webtty_fs_write", "rstream_webtty_fs_mkdir", "rstream_webtty_fs_delete"} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("tools/list missing %q: %s", want, string(payload))
		}
	}
	if !strings.Contains(string(payload), `"title":"Prepare rstream runtime"`) || !strings.Contains(string(payload), `"annotations"`) || !strings.Contains(string(payload), `"readOnlyHint"`) {
		t.Fatalf("tools/list does not expose MCP title and annotations: %s", string(payload))
	}
	var listed struct {
		Tools []struct {
			Annotations map[string]any `json:"annotations"`
			Name        string         `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(payload, &listed); err != nil {
		t.Fatalf("tools/list JSON is invalid: %v", err)
	}
	toolsByName := map[string]map[string]any{}
	for _, tool := range listed.Tools {
		toolsByName[tool.Name] = tool.Annotations
	}
	for _, check := range []struct {
		Key  string
		Name string
		Want bool
	}{{"destructiveHint", "rstream_webtty_exec", true}, {"destructiveHint", "rstream_webtty_fs_write", true}, {"destructiveHint", "rstream_remote_mcp_call", true}, {"openWorldHint", "rstream_project_list", true}, {"readOnlyHint", "rstream_project_creation_options", true}, {"readOnlyHint", "rstream_project_domain_connect", true}} {
		if got := toolsByName[check.Name][check.Key]; got != check.Want {
			t.Fatalf("%s %s = %#v, want %v", check.Name, check.Key, got, check.Want)
		}
	}
	if !strings.Contains(string(payload), `"outputSchema"`) || !strings.Contains(string(payload), `"login_url"`) || !strings.Contains(string(payload), `"suggested_next_tool"`) {
		t.Fatalf("tools/list does not expose key output schemas: %s", string(payload))
	}
	if !strings.Contains(string(payload), "read-only project token") || !strings.Contains(string(payload), "tunnels.resources.read-only requires list") {
		t.Fatalf("tools/list does not document token resource examples: %s", string(payload))
	}
	for _, want := range []string{"\"exec_path\"", "\"fs_path\""} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("tools/list missing WebTTY path argument %q: %s", want, string(payload))
		}
	}
}

func TestMCPAgentGuidanceIncludesEngineAuthBoundaries(t *testing.T) {
	guidance := mcpAgentGuidance()
	engineAuth, ok := guidance["engine_api_auth"].(map[string]any)
	if !ok {
		t.Fatalf("engine_api_auth guidance missing: %#v", guidance)
	}
	payload, err := json.Marshal(engineAuth)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	for _, want := range []string{"/api/clients", "/api/tunnels", "/api/sse", "/api/websocket", "rstream.token", "short-lived auth or app token", "watch-only/list resources"} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("engine auth guidance missing %q: %s", want, string(payload))
		}
	}
}

func TestMCPContextGetUsesSelectedEnvironmentContext(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`version: 1
defaults:
  context:
    name: prod
contexts:
  - name: prod
    apiUrl: https://rstream.io
    projectEndpoint: prod-endpoint
    engine: prod.c.rstream.io:443
  - name: tests
    apiUrl: http://localhost:3000
    projectEndpoint: test-endpoint
    engine: test.c.localhost.rstream.io:443
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	t.Setenv("RSTREAM_CONTEXT", "tests")
	contextResult, err := mcpContextGet(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpContextGet returned error: %v", err)
	}
	contextText := mcpResultText(t, contextResult)
	if !strings.Contains(contextText, `"Name": "tests"`) || !strings.Contains(contextText, `"ProjectEndpoint": "test-endpoint"`) {
		t.Fatalf("unexpected selected context: %s", contextText)
	}
	listResult, err := mcpContextList(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpContextList returned error: %v", err)
	}
	if listText := mcpResultText(t, listResult); !strings.Contains(listText, `"selected": "tests"`) {
		t.Fatalf("context list did not expose selected context: %s", listText)
	}
}

func TestMCPContextGetWithoutDefaultSuggestsRuntimePrepare(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`version: 1
environments:
  - apiUrl: https://rstream.io
    auth:
      token:
        storage:
          kind: inline
          value: login-token
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	contextResult, err := mcpContextGet(map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpContextGet returned error: %v", err)
	}
	contextText := mcpResultText(t, contextResult)
	if !strings.Contains(contextText, `"needs_context": true`) || !strings.Contains(contextText, `"suggested_next_tool": "rstream_runtime_prepare"`) {
		t.Fatalf("unexpected no-context result: %s", contextText)
	}
}

func TestMCPServeFlagsApplyEnvironmentOverrides(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("api-url", "", "")
	cmd.Flags().String("config", "", "")
	cmd.Flags().String("context", "", "")
	if err := cmd.Flags().Set("api-url", "http://localhost:3000"); err != nil {
		t.Fatalf("Set api-url returned error: %v", err)
	}
	if err := cmd.Flags().Set("config", "/tmp/rstream-test-config.yaml"); err != nil {
		t.Fatalf("Set config returned error: %v", err)
	}
	if err := cmd.Flags().Set("context", "tests"); err != nil {
		t.Fatalf("Set context returned error: %v", err)
	}
	t.Setenv("RSTREAM_API_URL", "https://rstream.io")
	t.Setenv("RSTREAM_CONFIG", "/tmp/original.yaml")
	t.Setenv("RSTREAM_CONTEXT", "prod")
	restore := applyMCPServeFlagEnvironment(cmd)
	if got := os.Getenv("RSTREAM_API_URL"); got != "http://localhost:3000" {
		t.Fatalf("RSTREAM_API_URL = %q", got)
	}
	if got := os.Getenv("RSTREAM_CONFIG"); got != "/tmp/rstream-test-config.yaml" {
		t.Fatalf("RSTREAM_CONFIG = %q", got)
	}
	if got := os.Getenv("RSTREAM_CONTEXT"); got != "tests" {
		t.Fatalf("RSTREAM_CONTEXT = %q", got)
	}
	restore()
	if got := os.Getenv("RSTREAM_API_URL"); got != "https://rstream.io" {
		t.Fatalf("restored RSTREAM_API_URL = %q", got)
	}
	if got := os.Getenv("RSTREAM_CONFIG"); got != "/tmp/original.yaml" {
		t.Fatalf("restored RSTREAM_CONFIG = %q", got)
	}
	if got := os.Getenv("RSTREAM_CONTEXT"); got != "prod" {
		t.Fatalf("restored RSTREAM_CONTEXT = %q", got)
	}
}

func TestMCPTokenCreateErrorPayloadIncludesResourceHelp(t *testing.T) {
	payload := mcpTokenCreateErrorPayload(errors.New("Invalid input").Error())
	examples, ok := payload["examples"].(map[string]string)
	if payload["ok"] != false || !ok || !strings.Contains(examples["read_only_project"], `"list":true`) || !strings.Contains(payload["hint"].(string), "scopes.tunnels") {
		t.Fatalf("unexpected token create error payload: %#v", payload)
	}
}

func TestMCPTokenCreateResultPayloadIncludesMetadata(t *testing.T) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"type":"auth","iat":10,"exp":130,"permissions":["tunnels.resources.read-only"],"resources":{"tunnels":{"projects":["project-1"]}}}`))
	payload := mcpTokenCreateResultPayload(controlplane.CreateTokenResponse{Token: header + "." + claims + "."})
	if payload["token_type"] != "auth" || payload["ttl_seconds"] != int64(120) || payload["expires_at"] != "1970-01-01T00:02:10Z" {
		t.Fatalf("unexpected token metadata: %#v", payload)
	}
	if payload["permissions"] == nil || payload["resources"] == nil {
		t.Fatalf("token metadata should include permissions and resources: %#v", payload)
	}
}

func TestMCPProjectListIgnoresExpiredDefaultContextWhenLoginTokenExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/tunnels" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer login-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(controlplane.ListProjectsResponse{Projects: []controlplane.Project{{ID: "p1", Name: "Prod", Endpoint: "abc12345", Domain: "cluster.example.com", EnginePort: 443, Status: "active", Plan: "pro"}}})
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`version: 1
defaults:
  context:
    name: Prod
environments:
  - apiUrl: %s
    auth:
      token:
        storage:
          kind: inline
          value: login-token
contexts:
  - name: Prod
    apiUrl: %s
    projectEndpoint: abc12345
    engine: abc12345.cluster.example.com:443
    auth:
      token:
        storage:
          kind: inline
          value: %s
`, server.URL, server.URL, mcpTestUnsignedJWT(`{"exp":100}`))), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	result, err := mcpProjectList(t.Context(), map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpProjectList returned error: %v", err)
	}
	if text := mcpResultText(t, result); !strings.Contains(text, `"name": "Prod"`) {
		t.Fatalf("unexpected project list: %s", text)
	}
}

func TestMCPRuntimePrepareUsesLoginTokenInsteadOfShortContextToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/tunnels" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer login-token" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(controlplane.ListProjectsResponse{Projects: []controlplane.Project{{ID: "p1", WorkspaceID: "w1", Name: "Prod", Endpoint: "abc12345", Domain: "cluster.example.com", EnginePort: 443, Status: "active", Plan: "pro", Region: "eu-west-3", TurnPort: 3478, TurnsPort: 5349}}})
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`version: 1
defaults:
  context:
    name: Prod
environments:
  - apiUrl: %s
    auth:
      token:
        storage:
          kind: inline
          value: login-token
contexts:
  - name: Prod
    apiUrl: %s
    projectEndpoint: old
    engine: old.cluster.example.com:443
    auth:
      token:
        storage:
          kind: inline
          value: %s
`, server.URL, server.URL, mcpTestUnsignedJWT(`{"exp":100}`))), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	result, err := mcpRuntimePrepare(t.Context(), map[string]json.RawMessage{"project": json.RawMessage(`"Prod"`)})
	if err != nil {
		t.Fatalf("mcpRuntimePrepare returned error: %v", err)
	}
	text := mcpResultText(t, result)
	if !strings.Contains(text, "no short-lived delegated token was minted") || !strings.Contains(text, `"engine": "abc12345.cluster.example.com:443"`) {
		t.Fatalf("unexpected runtime prepare payload: %s", text)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	contextValue, _, err := cfg.FindContextByName("Prod")
	if err != nil || contextValue == nil {
		t.Fatalf("FindContextByName returned %#v, %v", contextValue, err)
	}
	if contextValue.Auth != nil || contextValue.Engine != "abc12345.cluster.example.com:443" || contextValue.ProjectEndpoint != "abc12345" {
		t.Fatalf("unexpected prepared context: %#v", contextValue)
	}
	resolved, err := config.Resolve(config.ResolveInput{Config: cfg, EnvAPIURL: server.URL, RequireEngine: true, RequireToken: true, ResolveToken: true})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Token != "login-token" || resolved.Engine != "abc12345.cluster.example.com:443" {
		t.Fatalf("unexpected resolved runtime: %#v", resolved)
	}
}

func TestMCPRuntimeStatusSuggestsPrepareWhenLoginTokenHasNoContext(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`version: 1
environments:
  - apiUrl: https://rstream.example.test
    auth:
      token:
        storage:
          kind: inline
          value: login-token
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	result, err := mcpRuntimeStatus()
	if err != nil {
		t.Fatalf("mcpRuntimeStatus returned error: %v", err)
	}
	text := mcpResultText(t, result)
	if !strings.Contains(text, `"ready": false`) || !strings.Contains(text, `"needs_context": true`) || !strings.Contains(text, `"suggested_next_tool": "rstream_runtime_prepare"`) {
		t.Fatalf("unexpected runtime status: %s", text)
	}
}

func mcpTestUnsignedJWT(payload string) string {
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".signature"
}

func TestMCPPersonalWorkspaceMembersPayloadIsStructured(t *testing.T) {
	payload := mcpPersonalWorkspaceMembersPayload("workspace-1")
	members, ok := payload["members"].([]any)
	if payload["ok"] != true || payload["workspace_type"] != "personal" || !ok || len(members) != 0 {
		t.Fatalf("unexpected personal workspace members payload: %#v", payload)
	}
}

func TestMCPInitializeProtocolVersion(t *testing.T) {
	response := handleMCPMessage(t.Context(), mcpMessage{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize"})
	if response.Error != nil {
		t.Fatalf("unexpected error: %#v", response.Error)
	}
	payload, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if !strings.Contains(string(payload), `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize returned unexpected protocol version: %s", string(payload))
	}
	if !strings.Contains(string(payload), `"title":"rstream"`) || !strings.Contains(string(payload), "rstream_runtime_prepare") || !strings.Contains(string(payload), "call rstream_auth_poll with wait=true") || !strings.Contains(string(payload), "do not infer or recommend") {
		t.Fatalf("initialize missing display title or instructions: %s", string(payload))
	}
}

func TestMCPArgumentHelpers(t *testing.T) {
	args := map[string]json.RawMessage{
		"empty": json.RawMessage(`""`),
		"name":  json.RawMessage(`" shell "`),
		"list":  json.RawMessage(`[" a ","","b"]`),
	}
	empty, err := mcpStringArg(args, "empty")
	if err != nil || empty != "" {
		t.Fatalf("mcpStringArg(empty) = %q, %v", empty, err)
	}
	value, err := mcpRequiredStringArg(args, "name")
	if err != nil || value != " shell " {
		t.Fatalf("mcpRequiredStringArg = %q, %v", value, err)
	}
	values, err := mcpRequiredStringSliceArg(args, "list")
	if err != nil || len(values) != 2 || values[0] != "a" || values[1] != "b" {
		t.Fatalf("mcpRequiredStringSliceArg = %#v, %v", values, err)
	}
	if _, err := mcpRequiredStringArg(args, "missing"); err == nil {
		t.Fatalf("expected missing string argument error")
	}
	if _, err := mcpStringArg(args, "missing"); err == nil {
		t.Fatalf("expected missing raw string argument error")
	}
	boolValue, err := mcpOptionalBoolArg(map[string]json.RawMessage{"flag": json.RawMessage(`true`)}, "flag", false)
	if err != nil || !boolValue {
		t.Fatalf("mcpOptionalBoolArg = %v, %v", boolValue, err)
	}
	intValue, err := mcpOptionalIntArg(map[string]json.RawMessage{"page": json.RawMessage(`2`)}, "page")
	if err != nil || intValue == nil || *intValue != 2 {
		t.Fatalf("mcpOptionalIntArg = %#v, %v", intValue, err)
	}
}

func TestMCPWebTTYFilterArgs(t *testing.T) {
	for _, tc := range []struct {
		Args       map[string]json.RawMessage
		WantFilter string
		WantName   string
	}{
		{Args: map[string]json.RawMessage{"filter": json.RawMessage(`"shell"`)}, WantName: "shell"},
		{Args: map[string]json.RawMessage{"filter": json.RawMessage(`"labels.site=lab"`)}, WantFilter: "labels.site=lab"},
		{Args: map[string]json.RawMessage{"name": json.RawMessage(`"shell"`)}, WantName: "shell"},
		{Args: map[string]json.RawMessage{"filter": json.RawMessage(`"labels.site=lab"`), "name": json.RawMessage(`"shell"`)}, WantFilter: "labels.site=lab", WantName: "shell"},
	} {
		gotFilter, gotName, err := mcpWebTTYFilterArgs(tc.Args)
		if err != nil {
			t.Fatalf("mcpWebTTYFilterArgs returned error: %v", err)
		}
		if gotFilter != tc.WantFilter || gotName != tc.WantName {
			t.Fatalf("mcpWebTTYFilterArgs(%#v) = %q, %q; want %q, %q", tc.Args, gotFilter, gotName, tc.WantFilter, tc.WantName)
		}
	}
}

func TestMCPJSONResourceLinkResultIncludesStructuredContentAndLink(t *testing.T) {
	result, err := mcpJSONResourceLinkResult(map[string]any{"url": "https://local-tunnel.example.com"}, false, "https://local-tunnel.example.com", "local tunnel", "Public local tunnel", "text/html")
	if err != nil {
		t.Fatalf("mcpJSONResourceLinkResult returned error: %v", err)
	}
	content, ok := result["content"].([]map[string]any)
	if !ok || len(content) != 2 {
		t.Fatalf("unexpected content: %#v", result["content"])
	}
	if content[1]["type"] != "resource_link" || content[1]["uri"] != "https://local-tunnel.example.com" {
		t.Fatalf("unexpected resource link: %#v", content[1])
	}
	if structured, ok := result["structuredContent"].(map[string]any); !ok || structured["url"] != "https://local-tunnel.example.com" {
		t.Fatalf("unexpected structured content: %#v", result["structuredContent"])
	}
}

func TestMCPJSONResultUsesStructuredObjectForStructs(t *testing.T) {
	type response struct {
		Ready bool   `json:"ready"`
		Name  string `json:"name"`
	}
	result, err := mcpJSONResult(response{Ready: true, Name: "prod"}, false)
	if err != nil {
		t.Fatalf("mcpJSONResult returned error: %v", err)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structured content is not an object: %#v", result["structuredContent"])
	}
	if structured["result"] != nil {
		t.Fatalf("structured content should not wrap object results: %#v", structured)
	}
	if structured["ready"] != true || structured["name"] != "prod" {
		t.Fatalf("unexpected structured content: %#v", structured)
	}
}

func TestMCPContentReaderSupportsTextAndBase64(t *testing.T) {
	reader, err := mcpContentReader(map[string]json.RawMessage{}, "hello")
	if err != nil {
		t.Fatalf("mcpContentReader(text) returned error: %v", err)
	}
	data := bytes.Buffer{}
	if _, err := data.ReadFrom(reader); err != nil {
		t.Fatalf("failed to read text content: %v", err)
	}
	if data.String() != "hello" {
		t.Fatalf("text content = %q", data.String())
	}
	reader, err = mcpContentReader(map[string]json.RawMessage{"encoding": json.RawMessage(`"base64"`)}, "aGVsbG8=")
	if err != nil {
		t.Fatalf("mcpContentReader(base64) returned error: %v", err)
	}
	data.Reset()
	if _, err := data.ReadFrom(reader); err != nil {
		t.Fatalf("failed to read base64 content: %v", err)
	}
	if data.String() != "hello" {
		t.Fatalf("base64 content = %q", data.String())
	}
	if _, err := mcpContentReader(map[string]json.RawMessage{"encoding": json.RawMessage(`"gzip"`)}, "hello"); err == nil {
		t.Fatalf("expected invalid encoding error")
	}
}

func TestMCPCreateProjectArgsRequiresExplicitBillingInputs(t *testing.T) {
	args := map[string]json.RawMessage{"workspace_id": json.RawMessage(`"ws1"`), "name": json.RawMessage(`"Codex Demo"`), "provider": json.RawMessage(`"aws"`), "region": json.RawMessage(`"eu-west-3"`), "plan": json.RawMessage(`"basic"`), "creation_fingerprint": json.RawMessage(`"fingerprint"`)}
	workspaceID, request, err := mcpCreateProjectArgs(args)
	if err != nil {
		t.Fatalf("mcpCreateProjectArgs returned error: %v", err)
	}
	if workspaceID != "ws1" || request.Name != "Codex Demo" || request.Provider != "aws" || request.Region != "eu-west-3" || request.Plan != "basic" || request.CreationFingerprint != "fingerprint" {
		t.Fatalf("unexpected request: workspace=%q request=%#v", workspaceID, request)
	}
	if !strings.HasPrefix(request.IdempotencyKey, "mcp:") {
		t.Fatalf("missing generated idempotency key: %q", request.IdempotencyKey)
	}
	delete(args, "creation_fingerprint")
	if _, _, err := mcpCreateProjectArgs(args); err == nil {
		t.Fatalf("expected missing creation fingerprint error")
	}
}

func TestMCPControlPlaneArgs(t *testing.T) {
	args := map[string]json.RawMessage{"timeline": json.RawMessage(`"1h"`), "event_type": json.RawMessage(`"connection.closed"`), "page_size": json.RawMessage(`5`)}
	logs, err := mcpProjectLogsParams(args)
	if err != nil {
		t.Fatalf("mcpProjectLogsParams returned error: %v", err)
	}
	if logs.Timeline != "1h" || logs.EventType != "connection.closed" || logs.PageSize == nil || *logs.PageSize != 5 {
		t.Fatalf("unexpected logs params: %#v", logs)
	}
	domains, err := mcpListProjectDomainsParams(map[string]json.RawMessage{"q": json.RawMessage(`"codex"`), "page_size": json.RawMessage(`10`)})
	if err != nil {
		t.Fatalf("mcpListProjectDomainsParams returned error: %v", err)
	}
	if domains.Query != "codex" || domains.PageSize == nil || *domains.PageSize != 10 {
		t.Fatalf("unexpected domains params: %#v", domains)
	}
	settings, err := mcpProjectSettingsPatchArg(map[string]json.RawMessage{"settings": json.RawMessage(`{"publicAccessPolicy":"forbidden"}`)})
	if err != nil {
		t.Fatalf("mcpProjectSettingsPatchArg(object) returned error: %v", err)
	}
	if settings["publicAccessPolicy"] != "forbidden" {
		t.Fatalf("unexpected settings: %#v", settings)
	}
	settings, err = mcpProjectSettingsPatchArg(map[string]json.RawMessage{"settings_json": json.RawMessage(`"{\"minimumTlsVersion\":\"tls1.3\"}"`)})
	if err != nil {
		t.Fatalf("mcpProjectSettingsPatchArg(json) returned error: %v", err)
	}
	if settings["minimumTlsVersion"] != "tls1.3" {
		t.Fatalf("unexpected settings json: %#v", settings)
	}
	members, err := mcpWorkspaceMembersParams(map[string]json.RawMessage{"q": json.RawMessage(`"admin"`), "page_size": json.RawMessage(`10`)})
	if err != nil {
		t.Fatalf("mcpWorkspaceMembersParams returned error: %v", err)
	}
	if members.Query != "admin" || members.PageSize == nil || *members.PageSize != 10 {
		t.Fatalf("unexpected members params: %#v", members)
	}
}
