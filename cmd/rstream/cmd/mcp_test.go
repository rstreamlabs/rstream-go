// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, want := range []string{"rstream_auth_poll", "rstream_auth_start", "rstream_context_list", "rstream_context_get", "rstream_project_creation_options", "rstream_project_create", "rstream_project_list", "rstream_project_logs", "rstream_project_usage", "rstream_project_plan_get", "rstream_project_turn_usage", "rstream_project_turn_credentials_create", "rstream_project_domains_list", "rstream_project_domain_create", "rstream_project_domain_get", "rstream_project_domain_delete", "rstream_project_domain_verify", "rstream_project_domain_connect", "rstream_project_settings_get", "rstream_project_settings_patch", "rstream_project_settings_reset", "rstream_preview_expose", "rstream_preview_list", "rstream_preview_stop", "rstream_remote_expose", "rstream_remote_expose_stop", "rstream_remote_mcp_discover", "rstream_remote_mcp_tools", "rstream_remote_mcp_call", "rstream_runtime_status", "rstream_token_create", "rstream_workspace_list", "rstream_workspace_members_list", "rstream_webtty_list", "rstream_webtty_exec", "rstream_webtty_fs_list", "rstream_webtty_fs_read", "rstream_webtty_fs_write", "rstream_webtty_fs_mkdir", "rstream_webtty_fs_delete"} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("tools/list missing %q: %s", want, string(payload))
		}
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
	args := map[string]json.RawMessage{"workspace_id": json.RawMessage(`"ws1"`), "name": json.RawMessage(`"Codex Preview"`), "provider": json.RawMessage(`"aws"`), "region": json.RawMessage(`"eu-west-3"`), "plan": json.RawMessage(`"basic"`), "creation_fingerprint": json.RawMessage(`"fingerprint"`)}
	workspaceID, request, err := mcpCreateProjectArgs(args)
	if err != nil {
		t.Fatalf("mcpCreateProjectArgs returned error: %v", err)
	}
	if workspaceID != "ws1" || request.Name != "Codex Preview" || request.Provider != "aws" || request.Region != "eu-west-3" || request.Plan != "basic" || request.CreationFingerprint != "fingerprint" {
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
