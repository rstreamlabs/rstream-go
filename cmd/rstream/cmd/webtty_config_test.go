// See LICENSE file in the project root for license information.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyWebTTYServerRuntimeConfigAppliesServerAndE2EFlags(t *testing.T) {
	t.Setenv(webTTYConfigEnv, "")
	configPath := writeWebTTYRuntimeConfigFixture(t, `version: 1
server:
  serverId: prod-shell
  transport: plain
  executionMode: login
  loginUser: operator
  retry: true
  retryIntervalMs: 2500
  labels:
    env: production
    role: bastion
e2e:
  enabled: true
  identity: prod-shell
  identityFile: /var/lib/rstream/webtty.identity.json
  authorizedClientKeys:
    - client-key-id:client-public-key`)
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("webtty-config", configPath); err != nil {
		t.Fatalf("failed to set --webtty-config: %v", err)
	}
	if err := applyWebTTYServerRuntimeConfig(cmd); err != nil {
		t.Fatalf("applyWebTTYServerRuntimeConfig() error = %v", err)
	}
	if !webttyServerUsesRstream(cmd) {
		t.Fatalf("serverId from config should imply rstream mode")
	}
	if got, _ := cmd.Flags().GetString("server-id"); got != "prod-shell" {
		t.Fatalf("server-id = %q", got)
	}
	if got, _ := cmd.Flags().GetString("transport"); got != "plain" {
		t.Fatalf("transport = %q", got)
	}
	if got, _ := cmd.Flags().GetString("execution-mode"); got != "login" {
		t.Fatalf("execution-mode = %q", got)
	}
	if got, _ := cmd.Flags().GetString("login-user"); got != "operator" {
		t.Fatalf("login-user = %q", got)
	}
	if got := getBoolPtr(cmd, "retry"); got == nil || !*got {
		t.Fatalf("retry flag was not applied")
	}
	if got, _ := cmd.Flags().GetInt64("retry-interval"); got != 2500 {
		t.Fatalf("retry-interval = %d", got)
	}
	if got, _ := cmd.Flags().GetBool("e2e"); !got {
		t.Fatalf("e2e flag was not applied")
	}
	if got, _ := cmd.Flags().GetString("identity"); got != "prod-shell" {
		t.Fatalf("identity = %q", got)
	}
	keys, _ := cmd.Flags().GetStringArray("authorized-client-key")
	if len(keys) != 1 || keys[0] != "client-key-id:client-public-key" {
		t.Fatalf("authorized-client-key = %#v", keys)
	}
	labels := getStringArrayMap(cmd, "label")
	if labels["env"] != "production" || labels["role"] != "bastion" {
		t.Fatalf("labels = %#v", labels)
	}
}

func TestApplyWebTTYServerRuntimeConfigKeepsExplicitFlags(t *testing.T) {
	t.Setenv(webTTYConfigEnv, "")
	configPath := writeWebTTYRuntimeConfigFixture(t, `version: 1
server:
  serverId: config-shell
  transport: plain
  labels:
    env: production`)
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("webtty-config", configPath); err != nil {
		t.Fatalf("failed to set --webtty-config: %v", err)
	}
	if err := cmd.Flags().Set("server-id", "cli-shell"); err != nil {
		t.Fatalf("failed to set --server-id: %v", err)
	}
	if err := cmd.Flags().Set("transport", "websocket"); err != nil {
		t.Fatalf("failed to set --transport: %v", err)
	}
	if err := cmd.Flags().Set("label", "env=cli"); err != nil {
		t.Fatalf("failed to set --label: %v", err)
	}
	if err := applyWebTTYServerRuntimeConfig(cmd); err != nil {
		t.Fatalf("applyWebTTYServerRuntimeConfig() error = %v", err)
	}
	if got, _ := cmd.Flags().GetString("server-id"); got != "cli-shell" {
		t.Fatalf("server-id = %q", got)
	}
	if got, _ := cmd.Flags().GetString("transport"); got != "websocket" {
		t.Fatalf("transport = %q", got)
	}
	if labels := getStringArrayMap(cmd, "label"); labels["env"] != "cli" {
		t.Fatalf("labels = %#v", labels)
	}
}

func TestApplyWebTTYServerRuntimeConfigAndDerivedDefaults(t *testing.T) {
	t.Setenv(webTTYConfigEnv, "")
	configPath := writeWebTTYRuntimeConfigFixture(t, `version: 1
server:
  serverId: prod-shell`)
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("webtty-config", configPath); err != nil {
		t.Fatalf("failed to set --webtty-config: %v", err)
	}
	if err := applyWebTTYServerRuntimeConfig(cmd); err != nil {
		t.Fatalf("applyWebTTYServerRuntimeConfig() error = %v", err)
	}
	if err := applyWebTTYServerDerivedDefaults(cmd); err != nil {
		t.Fatalf("applyWebTTYServerDerivedDefaults() error = %v", err)
	}
	if got, _ := cmd.Flags().GetString("execution-mode"); got != "login" {
		t.Fatalf("execution-mode = %q, want login", got)
	}
	err := validateWebTTYServerFlags(cmd)
	if err == nil || !strings.Contains(err.Error(), "login execution mode requires --login-user") {
		t.Fatalf("validateWebTTYServerFlags() error = %v", err)
	}
}

func TestApplyWebTTYServerRuntimeConfigRejectsServerIDWithRstreamFalse(t *testing.T) {
	t.Setenv(webTTYConfigEnv, "")
	configPath := writeWebTTYRuntimeConfigFixture(t, `version: 1
server:
  rstream: false
  serverId: prod-shell`)
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("webtty-config", configPath); err != nil {
		t.Fatalf("failed to set --webtty-config: %v", err)
	}
	err := applyWebTTYServerRuntimeConfig(cmd)
	if err == nil || !strings.Contains(err.Error(), "imply rstream mode") {
		t.Fatalf("applyWebTTYServerRuntimeConfig() error = %v", err)
	}
}

func TestApplyWebTTYServerRuntimeConfigRejectsUnknownFields(t *testing.T) {
	t.Setenv(webTTYConfigEnv, "")
	configPath := writeWebTTYRuntimeConfigFixture(t, `version: 1
server:
  serverId: prod-shell
unknown: true`)
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("webtty-config", configPath); err != nil {
		t.Fatalf("failed to set --webtty-config: %v", err)
	}
	err := applyWebTTYServerRuntimeConfig(cmd)
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("applyWebTTYServerRuntimeConfig() error = %v", err)
	}
}

func TestApplyWebTTYServerRuntimeConfigSurfacesE2EFilesystemConflict(t *testing.T) {
	t.Setenv(webTTYConfigEnv, "")
	configPath := writeWebTTYRuntimeConfigFixture(t, `version: 1
filesystem:
  root: /tmp
e2e:
  enabled: true`)
	cmd := newTestWebTTYServerCommand()
	if err := cmd.Flags().Set("webtty-config", configPath); err != nil {
		t.Fatalf("failed to set --webtty-config: %v", err)
	}
	if err := applyWebTTYServerRuntimeConfig(cmd); err != nil {
		t.Fatalf("applyWebTTYServerRuntimeConfig() error = %v", err)
	}
	err := validateWebTTYServerFlags(cmd)
	if err == nil || !strings.Contains(err.Error(), "filesystem sidecar") {
		t.Fatalf("validateWebTTYServerFlags() error = %v", err)
	}
}

func TestApplyWebTTYServerRuntimeConfigUsesEnvironmentPath(t *testing.T) {
	configPath := writeWebTTYRuntimeConfigFixture(t, `version: 1
server:
  serverId: env-shell`)
	t.Setenv(webTTYConfigEnv, configPath)
	cmd := newTestWebTTYServerCommand()
	if err := applyWebTTYServerRuntimeConfig(cmd); err != nil {
		t.Fatalf("applyWebTTYServerRuntimeConfig() error = %v", err)
	}
	if got, _ := cmd.Flags().GetString("server-id"); got != "env-shell" {
		t.Fatalf("server-id = %q", got)
	}
}

func writeWebTTYRuntimeConfigFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webtty.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}
	return path
}
