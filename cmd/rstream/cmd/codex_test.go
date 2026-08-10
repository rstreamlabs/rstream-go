// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestCodexRstreamMCPBlock(t *testing.T) {
	clearCodexRuntimeOverrides(t)
	block := codexRstreamMCPBlock(`/tmp/rstream`)
	for _, want := range []string{`[mcp_servers.rstream]`, `command = "/tmp/rstream"`, `args = ["mcp", "serve"]`, `startup_timeout_sec = 30`, `tool_timeout_sec = 300`} {
		if !strings.Contains(block, want) {
			t.Fatalf("block missing %q:\n%s", want, block)
		}
	}
}

func TestCodexRstreamMCPBlockPreservesRuntimeOverrides(t *testing.T) {
	t.Setenv("RSTREAM_API_URL", "http://localhost:3000")
	t.Setenv("RSTREAM_CONFIG", `/tmp/rstream config/config.yaml`)
	t.Setenv("RSTREAM_CONTEXT", "tests")
	block := codexRstreamMCPBlock(`/tmp/rstream`)
	want := `env = { RSTREAM_API_URL = "http://localhost:3000", RSTREAM_CONFIG = "/tmp/rstream config/config.yaml", RSTREAM_CONTEXT = "tests" }`
	if !strings.Contains(block, want) {
		t.Fatalf("block missing rstream runtime env:\n%s", block)
	}
}

func TestReplaceTomlTreeMainTableOnly(t *testing.T) {
	content := "model = \"gpt-5\"\n\n[mcp_servers.rstream]\ncommand = \"old\"\n\n[mcp_servers.other]\ncommand = \"other\"\n"
	block := "[mcp_servers.rstream]\ncommand = \"new\"\n"
	next, replaced, err := replaceTomlTree(content, []string{"mcp_servers", "rstream"}, block)
	if err != nil || !replaced {
		t.Fatalf("replaceTomlTree replaced=%v err=%v", replaced, err)
	}
	if strings.Contains(next, `command = "old"`) || !strings.Contains(next, `command = "new"`) {
		t.Fatalf("main table was not replaced:\n%s", next)
	}
	if !strings.Contains(next, "model = \"gpt-5\"") || !strings.Contains(next, "[mcp_servers.other]\ncommand = \"other\"") {
		t.Fatalf("unrelated settings were not preserved:\n%s", next)
	}
}

func TestReplaceTomlTreeRemovesImmediateAndDistantDescendants(t *testing.T) {
	content := `# keep top
[mcp_servers.rstream]
command = "/missing/old"
args = ["mcp", "serve"]

[mcp_servers.rstream.env]
RSTREAM_CONFIG = "/missing/config.yaml"

# keep other server comment
[mcp_servers.other]
command = "other"
custom = true

# old rstream metadata
["mcp_servers"."rstream".metadata]
source = "old"

[mcp_servers.third]
command = "third"
`
	block := codexRstreamMCPBlockWithEnv("/bin/sh", nil)
	next, replaced, err := replaceTomlTree(content, []string{"mcp_servers", "rstream"}, block)
	if err != nil || !replaced {
		t.Fatalf("replaceTomlTree replaced=%v err=%v", replaced, err)
	}
	for _, unwanted := range []string{"[mcp_servers.rstream.env]", `RSTREAM_CONFIG = "/missing/config.yaml"`, `["mcp_servers"."rstream".metadata]`, `source = "old"`} {
		if strings.Contains(next, unwanted) {
			t.Fatalf("replacement retained %q:\n%s", unwanted, next)
		}
	}
	for _, preserved := range []string{"# keep top", "# keep other server comment", "[mcp_servers.other]", `custom = true`, "[mcp_servers.third]"} {
		if !strings.Contains(next, preserved) {
			t.Fatalf("replacement lost %q:\n%s", preserved, next)
		}
	}
	if strings.Count(next, "[mcp_servers.rstream]") != 1 {
		t.Fatalf("replacement should contain one main table:\n%s", next)
	}
	if _, _, err := parseCodexRstreamConfig(next); err != nil {
		t.Fatalf("replacement produced invalid TOML: %v\n%s", err, next)
	}
}

func TestConfigureCodexRepairsMissingCommandAndStaleEnvTable(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	original := `model = "gpt-5"

[mcp_servers.rstream]
command = "/definitely/missing/rstream"
args = ["mcp", "serve"]
startup_timeout_sec = 30
tool_timeout_sec = 300

[mcp_servers.rstream.env]
RSTREAM_CONFIG = "/definitely/missing/config.yaml"
`
	mustWriteFile(t, configPath, original, 0o640)
	command := mustResolveCodexExecutable(t, "/bin/sh")
	result, err := configureCodexRstreamMCP(configPath, command, nil, codexRstreamMCPBlockWithEnv(command, nil), false)
	if err != nil {
		t.Fatalf("configureCodexRstreamMCP returned error: %v", err)
	}
	if !result.Changed || result.Status != "repaired" || !result.ReloadRequired {
		t.Fatalf("unexpected repair result: %#v", result)
	}
	updated := mustReadFile(t, configPath)
	if strings.Contains(updated, "mcp_servers.rstream.env") || strings.Contains(updated, "/definitely/missing") {
		t.Fatalf("stale command or env table survived repair:\n%s", updated)
	}
	if !strings.Contains(updated, "model = \"gpt-5\"") {
		t.Fatalf("unrelated setting was lost:\n%s", updated)
	}
	info, statErr := os.Stat(configPath)
	if statErr != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("permissions not preserved: mode=%v err=%v", info.Mode().Perm(), statErr)
	}
}

func TestConfigureCodexRepairsMissingRstreamConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	command := mustResolveCodexExecutable(t, "/bin/sh")
	original := codexRstreamMCPBlockWithEnv(command, nil) + "\n[mcp_servers.rstream.env]\nRSTREAM_CONFIG = \"/missing/rstream.yaml\"\n"
	mustWriteFile(t, configPath, original, 0o600)
	result, err := configureCodexRstreamMCP(configPath, command, nil, codexRstreamMCPBlockWithEnv(command, nil), false)
	if err != nil {
		t.Fatalf("configureCodexRstreamMCP returned error: %v", err)
	}
	if result.Status != "repaired" || !result.Changed {
		t.Fatalf("missing RSTREAM_CONFIG should be repaired: %#v", result)
	}
	if updated := mustReadFile(t, configPath); strings.Contains(updated, "RSTREAM_CONFIG") || strings.Contains(updated, ".env]") {
		t.Fatalf("stale RSTREAM_CONFIG survived repair:\n%s", updated)
	}
	if !hasDiagnostic(result.Diagnostics, "rstream_config_unreadable") {
		t.Fatalf("repair result lacks safe RSTREAM_CONFIG diagnostic: %#v", result.Diagnostics)
	}
}

func TestConfigureCodexForceRemovesMultipleChildTables(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	command := mustResolveCodexExecutable(t, "/bin/sh")
	original := `[mcp_servers.rstream]
command = "/custom/rstream"
args = ["custom"]

[mcp_servers.rstream.env]
RSTREAM_CONTEXT = "old"

[mcp_servers.keep]
command = "keep"

[mcp_servers.rstream.options]
mode = "custom"

[mcp_servers.rstream.options.deep]
enabled = true
`
	mustWriteFile(t, configPath, original, 0o600)
	result, err := configureCodexRstreamMCP(configPath, command, nil, codexRstreamMCPBlockWithEnv(command, nil), true)
	if err != nil || result.Status != "repaired" || !result.Changed {
		t.Fatalf("forced configuration result=%#v err=%v", result, err)
	}
	updated := mustReadFile(t, configPath)
	for _, unwanted := range []string{"mcp_servers.rstream.env", "mcp_servers.rstream.options", "RSTREAM_CONTEXT", `mode = "custom"`, `enabled = true`} {
		if strings.Contains(updated, unwanted) {
			t.Fatalf("forced replacement retained %q:\n%s", unwanted, updated)
		}
	}
	if !strings.Contains(updated, "[mcp_servers.keep]\ncommand = \"keep\"") {
		t.Fatalf("forced replacement lost other server:\n%s", updated)
	}
}

func TestConfigureCodexAlreadyCorrectAndIdempotent(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	command := mustResolveCodexExecutable(t, "/bin/sh")
	block := codexRstreamMCPBlockWithEnv(command, nil)
	first, err := configureCodexRstreamMCP(configPath, command, nil, block, false)
	if err != nil || first.Status != "installed" || !first.Changed {
		t.Fatalf("first setup result=%#v err=%v", first, err)
	}
	afterFirst := mustReadFile(t, configPath)
	second, err := configureCodexRstreamMCP(configPath, command, nil, block, false)
	if err != nil || second.Status != "already_configured" || second.Changed || second.ReloadRequired {
		t.Fatalf("second setup result=%#v err=%v", second, err)
	}
	if afterSecond := mustReadFile(t, configPath); afterSecond != afterFirst {
		t.Fatalf("idempotent setup changed bytes:\nfirst:\n%s\nsecond:\n%s", afterFirst, afterSecond)
	}
}

func TestConfigureCodexEquivalentEnvChildTableIsAlreadyConfigured(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	command := mustResolveCodexExecutable(t, "/bin/sh")
	rstreamConfig := filepath.Join(t.TempDir(), "rstream.yaml")
	mustWriteFile(t, rstreamConfig, "defaults: {}\n", 0o600)
	original := fmt.Sprintf(`[mcp_servers.rstream]
command = %s
args = ["mcp", "serve"]
startup_timeout_sec = 30
tool_timeout_sec = 300

[mcp_servers.rstream.env]
RSTREAM_CONFIG = %s
`, tomlString(command), tomlString(rstreamConfig))
	mustWriteFile(t, configPath, original, 0o600)
	result, err := configureCodexRstreamMCP(configPath, command, map[string]string{"RSTREAM_CONFIG": rstreamConfig}, codexRstreamMCPBlockWithEnv(command, map[string]string{"RSTREAM_CONFIG": rstreamConfig}), false)
	if err != nil || result.Status != "already_configured" || result.Changed {
		t.Fatalf("equivalent child env table result=%#v err=%v", result, err)
	}
	if updated := mustReadFile(t, configPath); updated != original {
		t.Fatalf("equivalent configuration should remain byte-identical:\n%s", updated)
	}
}

func TestConfigureCodexAppliesReadableExplicitRuntimeOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	command := mustResolveCodexExecutable(t, "/bin/sh")
	rstreamConfig := filepath.Join(t.TempDir(), "rstream.yaml")
	mustWriteFile(t, rstreamConfig, "defaults: {}\n", 0o600)
	mustWriteFile(t, configPath, codexRstreamMCPBlockWithEnv(command, nil), 0o600)
	desiredEnv := map[string]string{"RSTREAM_CONFIG": rstreamConfig}
	result, err := configureCodexRstreamMCP(configPath, command, desiredEnv, codexRstreamMCPBlockWithEnv(command, desiredEnv), false)
	if err != nil || result.Status != "repaired" || !result.Changed {
		t.Fatalf("explicit override result=%#v err=%v", result, err)
	}
	if updated := mustReadFile(t, configPath); !strings.Contains(updated, "RSTREAM_CONFIG = "+tomlString(rstreamConfig)) {
		t.Fatalf("explicit override was not applied:\n%s", updated)
	}
}

func TestConfigureCodexCustomConfigurationRequiresForce(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	desiredCommand := mustResolveCodexExecutable(t, "/bin/sh")
	customCommand := makeCodexExecutable(t, "exit 0")
	original := codexRstreamMCPBlockWithEnv(customCommand, nil)
	mustWriteFile(t, configPath, original, 0o600)
	result, err := configureCodexRstreamMCP(configPath, desiredCommand, nil, codexRstreamMCPBlockWithEnv(desiredCommand, nil), false)
	if err == nil || result.Status != "conflict" || result.Changed {
		t.Fatalf("custom configuration result=%#v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "--force") || !hasDiagnostic(result.Diagnostics, "force_required") {
		t.Fatalf("conflict is not actionable: result=%#v err=%v", result, err)
	}
	if updated := mustReadFile(t, configPath); updated != original {
		t.Fatalf("conflict changed configuration:\n%s", updated)
	}
}

func TestConfigureCodexPreservesOtherMCPServersCommentsAndSettings(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	command := mustResolveCodexExecutable(t, "/bin/sh")
	original := `# global comment
model = "gpt-5"
approval_policy = "never"

# keep alpha exactly
[mcp_servers.alpha]
command = "alpha"
args = ["--safe"] # inline comment

[mcp_servers.rstream]
command = "/missing/rstream"
args = ["mcp", "serve"]
startup_timeout_sec = 30
tool_timeout_sec = 300

# keep omega exactly
[mcp_servers.omega]
command = "omega"
`
	mustWriteFile(t, configPath, original, 0o600)
	_, err := configureCodexRstreamMCP(configPath, command, nil, codexRstreamMCPBlockWithEnv(command, nil), false)
	if err != nil {
		t.Fatalf("repair returned error: %v", err)
	}
	updated := mustReadFile(t, configPath)
	for _, preserved := range []string{
		"# global comment\nmodel = \"gpt-5\"\napproval_policy = \"never\"",
		"# keep alpha exactly\n[mcp_servers.alpha]\ncommand = \"alpha\"\nargs = [\"--safe\"] # inline comment",
		"# keep omega exactly\n[mcp_servers.omega]\ncommand = \"omega\"",
	} {
		if !strings.Contains(updated, preserved) {
			t.Fatalf("repair did not preserve fragment %q:\n%s", preserved, updated)
		}
	}
}

func TestRunCodexSetupStructuredOutputAndHumanReloadHint(t *testing.T) {
	clearCodexRuntimeOverrides(t)
	configPath := filepath.Join(t.TempDir(), "codex", "config.toml")
	command := newCodexSetupCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--config", configPath, "--command", "/bin/sh", "-o", "json"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("JSON setup returned error: %v", err)
	}
	var result codexSetupResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, output.String())
	}
	if !result.Changed || result.Status != "installed" || result.Config != configPath || result.Command == "" || !result.ReloadRequired || len(result.Diagnostics) == 0 {
		t.Fatalf("JSON output lacks required fields: %#v", result)
	}

	yamlConfig := filepath.Join(t.TempDir(), "config.toml")
	yamlCommand := newCodexSetupCommand()
	output.Reset()
	yamlCommand.SetOut(&output)
	yamlCommand.SetArgs([]string{"--config", yamlConfig, "--command", "/bin/sh", "-o", "yaml"})
	if err := yamlCommand.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("YAML setup returned error: %v", err)
	}
	var yamlResult codexSetupResult
	if err := yaml.Unmarshal(output.Bytes(), &yamlResult); err != nil {
		t.Fatalf("invalid YAML output: %v\n%s", err, output.String())
	}
	if !yamlResult.Changed || yamlResult.Status != "installed" || yamlResult.Config != yamlConfig || yamlResult.Command == "" || !yamlResult.ReloadRequired || len(yamlResult.Diagnostics) == 0 {
		t.Fatalf("YAML output lacks required fields: %#v", yamlResult)
	}

	humanConfig := filepath.Join(t.TempDir(), "config.toml")
	human := newCodexSetupCommand()
	output.Reset()
	human.SetOut(&output)
	human.SetArgs([]string{"--config", humanConfig, "--command", "/bin/sh"})
	if err := human.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("human setup returned error: %v", err)
	}
	if !strings.Contains(output.String(), "Open a new Codex task or reload Codex") {
		t.Fatalf("human output lacks reload guidance:\n%s", output.String())
	}
}

func TestRunCodexSetupRejectsMissingCommandAndExplicitConfigOverride(t *testing.T) {
	clearCodexRuntimeOverrides(t)
	configPath := filepath.Join(t.TempDir(), "config.toml")
	command := newCodexSetupCommand()
	command.SetOut(&bytes.Buffer{})
	command.SetArgs([]string{"--config", configPath, "--command", filepath.Join(t.TempDir(), "missing")})
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("missing command was not rejected safely: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("invalid command should not create config: %v", err)
	}

	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	command = newCodexSetupCommand()
	command.SetOut(&bytes.Buffer{})
	command.SetArgs([]string{"--config", configPath, "--command", "/bin/sh"})
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "RSTREAM_CONFIG") {
		t.Fatalf("missing explicit RSTREAM_CONFIG was not rejected safely: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("invalid override should not create config: %v", err)
	}
}

func TestCodexSetupDoesNotExposeSecrets(t *testing.T) {
	const secret = "token-super-secret-123"
	configPath := filepath.Join(t.TempDir(), "config.toml")
	command := mustResolveCodexExecutable(t, "/bin/sh")
	original := codexRstreamMCPBlockWithEnv(command, nil) + fmt.Sprintf("\n[mcp_servers.rstream.env]\nRSTREAM_TOKEN = %s\n", tomlString(secret))
	mustWriteFile(t, configPath, original, 0o600)
	result, err := configureCodexRstreamMCP(configPath, command, nil, codexRstreamMCPBlockWithEnv(command, nil), false)
	if err == nil {
		t.Fatal("secret-bearing custom configuration should require --force")
	}
	payload, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatalf("Marshal returned error: %v", marshalErr)
	}
	combined := string(payload) + err.Error()
	if strings.Contains(combined, secret) {
		t.Fatalf("secret leaked in result or error: %s", combined)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid.toml")
	mustWriteFile(t, invalidPath, "[mcp_servers.rstream]\ncommand = \""+secret+"\n", 0o600)
	_, parseErr := configureCodexRstreamMCP(invalidPath, command, nil, codexRstreamMCPBlockWithEnv(command, nil), false)
	if parseErr == nil || strings.Contains(parseErr.Error(), secret) {
		t.Fatalf("invalid TOML error leaked content: %v", parseErr)
	}
}

func TestVerifyCodexMCPHandshake(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper script uses a POSIX shell")
	}
	t.Setenv("GO_WANT_CODEX_MCP_HELPER", "1")
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable returned error: %v", err)
	}
	script := makeCodexExecutable(t, fmt.Sprintf("exec %s -test.run='^TestCodexMCPVerificationHelper$'", codexTestShellQuote(testBinary)))
	verification, err := verifyCodexMCP(t.Context(), script, []string{"mcp", "serve"}, nil, codexMCPVerifyTimeout)
	if err != nil {
		t.Fatalf("verifyCodexMCP returned error: %v", err)
	}
	if verification.Status != "verified" || verification.ProtocolVersion != mcpProtocolVersion || verification.ServerName != "rstream" || verification.ServerVersion == "" || verification.ToolCount < len(codexEssentialTools) {
		t.Fatalf("unexpected verification result: %#v", verification)
	}
	if !stringSlicesEqual(verification.RequiredTools, codexEssentialTools) {
		t.Fatalf("unexpected required tools: %#v", verification.RequiredTools)
	}
}

func TestVerifyCodexMCPDoesNotExposeServerStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper script uses a POSIX shell")
	}
	const secret = "server-token-secret"
	script := makeCodexExecutable(t, fmt.Sprintf("echo %s >&2\nexit 1", codexTestShellQuote(secret)))
	_, err := verifyCodexMCP(t.Context(), script, []string{"mcp", "serve"}, nil, time.Second)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("verification error exposed server stderr: %v", err)
	}
}

func TestCodexMCPVerificationHelper(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_MCP_HELPER") != "1" {
		return
	}
	if err := serveMCP(context.Background(), os.Stdin, os.Stdout); err != nil {
		t.Fatalf("serveMCP returned error: %v", err)
	}
}

func clearCodexRuntimeOverrides(t *testing.T) {
	t.Helper()
	for _, name := range []string{"RSTREAM_API_URL", "RSTREAM_CONFIG", "RSTREAM_CONTEXT"} {
		t.Setenv(name, "")
	}
}

func mustResolveCodexExecutable(t *testing.T, path string) string {
	t.Helper()
	resolved, err := resolveCodexExecutable(path)
	if err != nil {
		t.Fatalf("resolveCodexExecutable(%q) returned error: %v", path, err)
	}
	return resolved
}

func makeCodexExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex-mcp-helper")
	content := "#!/bin/sh\n" + body + "\n"
	mustWriteFile(t, path, content, 0o700)
	return path
}

func mustWriteFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	return string(data)
}

func hasDiagnostic(diagnostics []codexDiagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func codexTestShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
