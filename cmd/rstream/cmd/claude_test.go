// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"
	"testing"
)

func TestClaudeMCPAddArgs(t *testing.T) {
	args := claudeMCPAddArgs("rstream", "user", "/tmp/rstream", nil)
	got := strings.Join(args, " ")
	want := "mcp add rstream --scope user -- /tmp/rstream mcp serve"
	if got != want {
		t.Fatalf("claudeMCPAddArgs = %q, want %q", got, want)
	}
}

func TestClaudeMCPAddArgsIncludesEnv(t *testing.T) {
	args := claudeMCPAddArgs("rstream", "project", "/tmp/rstream", map[string]string{"RSTREAM_CONFIG": "/tmp/config.yaml"})
	got := strings.Join(args, " ")
	want := "mcp add rstream --scope project --env RSTREAM_CONFIG=/tmp/config.yaml -- /tmp/rstream mcp serve"
	if got != want {
		t.Fatalf("claudeMCPAddArgs = %q, want %q", got, want)
	}
}

func TestClaudeRstreamMCPJSON(t *testing.T) {
	out := claudeRstreamMCPJSON("rstream", "/tmp/rstream", nil)
	for _, want := range []string{`"mcpServers"`, `"rstream"`, `"type": "stdio"`, `"command": "/tmp/rstream"`, `"mcp"`, `"serve"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q:\n%s", want, out)
		}
	}
}

func TestClaudeRstreamMCPJSONIncludesEnv(t *testing.T) {
	out := claudeRstreamMCPJSON("rstream", "/tmp/rstream", map[string]string{"RSTREAM_CONFIG": "/tmp/config.yaml"})
	if !strings.Contains(out, `"RSTREAM_CONFIG": "/tmp/config.yaml"`) {
		t.Fatalf("json missing RSTREAM_CONFIG env:\n%s", out)
	}
}

func TestClaudeRstreamSetupPreviewHasBothForms(t *testing.T) {
	preview := claudeRstreamSetupPreview("rstream", "user", "/tmp/rstream", nil)
	if !strings.Contains(preview, `"mcpServers"`) {
		t.Fatalf("preview missing .mcp.json form:\n%s", preview)
	}
	if !strings.Contains(preview, "claude mcp add rstream --scope user -- /tmp/rstream mcp serve") {
		t.Fatalf("preview missing CLI form:\n%s", preview)
	}
}

func TestValidateClaudeScope(t *testing.T) {
	for _, scope := range []string{"local", "project", "user"} {
		if err := validateClaudeScope(scope); err != nil {
			t.Fatalf("validateClaudeScope(%q) returned error: %v", scope, err)
		}
	}
	if err := validateClaudeScope("global"); err == nil {
		t.Fatalf("validateClaudeScope(\"global\") should return an error")
	}
}

func TestClaudeShellArgQuotesSpaces(t *testing.T) {
	if got := claudeShellArg("/tmp/rstream config/config.yaml"); got != `'/tmp/rstream config/config.yaml'` {
		t.Fatalf("claudeShellArg returned %q", got)
	}
	if got := claudeShellArg("/tmp/rstream"); got != "/tmp/rstream" {
		t.Fatalf("claudeShellArg should not quote a plain path, got %q", got)
	}
}
