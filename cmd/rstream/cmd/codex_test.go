// See LICENSE file in the project root for license information.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexRstreamMCPBlock(t *testing.T) {
	block := codexRstreamMCPBlock(`/tmp/rstream`)
	for _, want := range []string{`[mcp_servers.rstream]`, `command = "/tmp/rstream"`, `args = ["mcp", "serve"]`, `startup_timeout_sec = 30`, `tool_timeout_sec = 300`} {
		if !strings.Contains(block, want) {
			t.Fatalf("block missing %q:\n%s", want, block)
		}
	}
}

func TestCodexRstreamMCPBlockPreservesExplicitRstreamConfig(t *testing.T) {
	t.Setenv("RSTREAM_CONFIG", `/tmp/rstream config/config.yaml`)
	block := codexRstreamMCPBlock(`/tmp/rstream`)
	if !strings.Contains(block, `env = { RSTREAM_CONFIG = "/tmp/rstream config/config.yaml" }`) {
		t.Fatalf("block missing RSTREAM_CONFIG env:\n%s", block)
	}
}

func TestCodexRstreamMCPBlockPreservesRstreamRuntimeOverrides(t *testing.T) {
	t.Setenv("RSTREAM_API_URL", "http://localhost:3000")
	t.Setenv("RSTREAM_CONFIG", `/tmp/rstream config/config.yaml`)
	t.Setenv("RSTREAM_CONTEXT", "tests")
	block := codexRstreamMCPBlock(`/tmp/rstream`)
	want := `env = { RSTREAM_API_URL = "http://localhost:3000", RSTREAM_CONFIG = "/tmp/rstream config/config.yaml", RSTREAM_CONTEXT = "tests" }`
	if !strings.Contains(block, want) {
		t.Fatalf("block missing rstream runtime env:\n%s", block)
	}
}

func TestUpsertTomlSection(t *testing.T) {
	content := "model = \"gpt-5\"\n\n[mcp_servers.old]\ncommand = \"old\"\n"
	block := "[mcp_servers.rstream]\ncommand = \"rstream\"\n"
	next, replaced, exists := upsertTomlSection(content, "[mcp_servers.rstream]", block, false)
	if replaced || exists || !strings.Contains(next, block) || !strings.Contains(next, "[mcp_servers.old]") {
		t.Fatalf("unexpected insert result: replaced=%v exists=%v\n%s", replaced, exists, next)
	}
	next, replaced, exists = upsertTomlSection(next, "[mcp_servers.rstream]", "[mcp_servers.rstream]\ncommand = \"new\"\n", false)
	if replaced || !exists || !strings.Contains(next, `command = "rstream"`) {
		t.Fatalf("existing section should be preserved without force: replaced=%v exists=%v\n%s", replaced, exists, next)
	}
	next, replaced, exists = upsertTomlSection(next, "[mcp_servers.rstream]", "[mcp_servers.rstream]\ncommand = \"new\"\n", true)
	if !replaced || !exists || !strings.Contains(next, `command = "new"`) || strings.Contains(next, `command = "rstream"`) {
		t.Fatalf("force should replace section: replaced=%v exists=%v\n%s", replaced, exists, next)
	}
}

func TestWriteCodexRstreamMCPConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	changed, err := writeCodexRstreamMCPConfig(configPath, codexRstreamMCPBlock("/bin/rstream"), false)
	if err != nil || !changed {
		t.Fatalf("writeCodexRstreamMCPConfig changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), `[mcp_servers.rstream]`) {
		t.Fatalf("config missing rstream section:\n%s", string(data))
	}
	changed, err = writeCodexRstreamMCPConfig(configPath, codexRstreamMCPBlock("/bin/new"), false)
	if err != nil || changed {
		t.Fatalf("existing config without force should not change: changed=%v err=%v", changed, err)
	}
}
