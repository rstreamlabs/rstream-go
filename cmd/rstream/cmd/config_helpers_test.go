// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func clearRstreamTestEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"RSTREAM_API_URL",
		"RSTREAM_AUTHENTICATION_TOKEN",
		"RSTREAM_CONFIG",
		"RSTREAM_CONTEXT",
		"RSTREAM_ENGINE",
		"RSTREAM_QUIC_TRANSPORT",
		"RSTREAM_REGION",
		"RSTREAM_TUNNEL_TRANSPORT",
		"RSTREAM_WEBTTY_AUTH_TOKEN",
		"RSTREAM_WEBTTY_CONFIG",
		"RSTREAM_WEBTTY_IDENTITY",
		"RSTREAM_WEBTTY_IDENTITY_FILE",
		"RSTREAM_WEBTTY_KNOWN_SERVER_KEY",
		"RSTREAM_WEBTTY_KNOWN_SERVERS_FILE",
	} {
		t.Setenv(key, "")
	}
}

func TestEnvironmentTokenHelpersAndOutputValidation(t *testing.T) {
	var env config.Environment
	if err := setEnvironmentToken(&env, "token-value"); err != nil {
		t.Fatalf("set token: %v", err)
	}
	if env.Auth == nil || env.Auth.Token == nil || env.Auth.Token.Storage == nil || env.Auth.Token.Storage.Value != "token-value" {
		t.Fatalf("token not stored inline: %#v", env.Auth)
	}
	clearEnvironmentToken(&env)
	if env.Auth != nil {
		t.Fatalf("auth should be cleared after token removal: %#v", env.Auth)
	}
	env.Auth = &config.Auth{
		Token: &config.Token{Storage: &config.TokenStorage{Kind: config.TokenStorageInline, Value: "token-value"}},
		MTLS:  &config.MTLS{CertificateFile: "client.crt", KeyFile: "client.key"},
	}
	clearEnvironmentToken(&env)
	if env.Auth == nil || env.Auth.Token != nil || env.Auth.MTLS == nil {
		t.Fatalf("mTLS auth should be preserved after token removal: %#v", env.Auth)
	}
	if err := setEnvironmentToken(&env, ""); err == nil {
		t.Fatalf("expected empty token error")
	}
	if err := validateOutputMode("json", "json", "yaml"); err != nil {
		t.Fatalf("valid output mode rejected: %v", err)
	}
	if err := validateOutputMode("xml", "json", "yaml"); err == nil || !strings.Contains(err.Error(), "invalid --output") {
		t.Fatalf("expected invalid output mode error, got %v", err)
	}
	if err := writeOptionalStructuredOutput("none", map[string]string{"ignored": "true"}); err != nil {
		t.Fatalf("none output should not write: %v", err)
	}
	if err := writeOptionalStructuredOutput("invalid", nil); err == nil {
		t.Fatalf("expected optional structured output validation error")
	}
}

func TestResolveConfigPathAndAPIURLPrecedence(t *testing.T) {
	clearRstreamTestEnv(t)
	command := runtimeFlagsCommand()
	flagPath := filepath.Join(t.TempDir(), "flag.yaml")
	envPath := filepath.Join(t.TempDir(), "env.yaml")
	mustSetFlag(t, command, "config", flagPath)
	t.Setenv("RSTREAM_CONFIG", envPath)
	path, err := resolveConfigPath(command)
	if err != nil || path != flagPath {
		t.Fatalf("resolveConfigPath(flag) = %q, %v", path, err)
	}
	command = runtimeFlagsCommand()
	t.Setenv("RSTREAM_CONFIG", envPath)
	path, err = resolveConfigPath(command)
	if err != nil || path != envPath {
		t.Fatalf("resolveConfigPath(env) = %q, %v", path, err)
	}
	mustSetFlag(t, command, "api-url", "https://flag.example.com")
	apiURL, err := resolveAPIURL(command, config.Config{})
	if err != nil || apiURL != "https://flag.example.com" {
		t.Fatalf("resolveAPIURL(flag) = %q, %v", apiURL, err)
	}
	command = runtimeFlagsCommand()
	t.Setenv("RSTREAM_API_URL", "https://env.example.com")
	apiURL, err = resolveAPIURL(command, config.Config{})
	if err != nil || apiURL != "https://env.example.com" {
		t.Fatalf("resolveAPIURL(env) = %q, %v", apiURL, err)
	}
}

func TestResolveRuntimeLoadsConfigTokenAndTransport(t *testing.T) {
	clearRstreamTestEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	apiURL := "https://api.example.com"
	cfg := config.Config{
		Defaults: config.Defaults{Context: &config.DefaultContext{Name: "demo"}},
		Environments: []config.Environment{{APIURL: apiURL, Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
			Kind:  config.TokenStorageInline,
			Value: "env-token",
		}}}}},
		Contexts: []config.Context{{Name: "demo", APIURL: apiURL, Engine: "engine.example.com:443"}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	command := runtimeFlagsCommand()
	t.Setenv("RSTREAM_CONFIG", path)
	t.Setenv("RSTREAM_QUIC_TRANSPORT", "1")
	runtime, err := resolveRuntime(command, true, true)
	if err != nil {
		t.Fatalf("resolveRuntime() error = %v", err)
	}
	if runtime.ConfigPath != path || runtime.Resolved.Engine != "engine.example.com:443" || runtime.Resolved.Token != "env-token" {
		t.Fatalf("unexpected runtime: %#v", runtime)
	}
	if _, ok := runtime.Resolved.Transport.(*rstream.QUICTransport); !ok {
		t.Fatalf("transport override = %T, want QUICTransport", runtime.Resolved.Transport)
	}
	command = runtimeFlagsCommand()
	mustSetFlag(t, command, "tunnel-transport", "tls")
	t.Setenv("RSTREAM_TUNNEL_TRANSPORT", "quic")
	runtime, err = resolveRuntime(command, true, true)
	if err != nil {
		t.Fatalf("resolveRuntime() with transport flag error = %v", err)
	}
	if _, ok := runtime.Resolved.Transport.(*rstream.Transport); !ok {
		t.Fatalf("flag transport override = %T, want Transport", runtime.Resolved.Transport)
	}
	client, err := newClientFromResolved(runtime.Resolved)
	if err != nil || client == nil {
		t.Fatalf("newClientFromResolved() = %v, %v", client, err)
	}
}

func TestResolveRuntimeSelectsAuthorizedRegion(t *testing.T) {
	clearRstreamTestEnv(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/api/projects/tunnels/resolve/project" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer control-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(controlplane.Project{
			Endpoint: "project",
			RegionalEndpoints: []controlplane.ProjectRegionalEndpoint{
				{Provider: "aws", Region: "eu-west-3", Domain: "eu.example.test", EnginePort: 8443},
				{Provider: "aws", Region: "us-east-1", Domain: "us.example.test", EnginePort: 443},
			},
		})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Defaults: config.Defaults{Context: &config.DefaultContext{Name: "global"}},
		Environments: []config.Environment{{APIURL: server.URL, Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
			Kind:  config.TokenStorageInline,
			Value: "control-token",
		}}}}},
		Contexts: []config.Context{{Name: "global", APIURL: server.URL, Engine: "project.global.example.test:443", ProjectEndpoint: "project"}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	command := runtimeFlagsCommand()
	mustSetFlag(t, command, "region", "US-EAST-1")
	t.Setenv("RSTREAM_CONFIG", path)
	runtime, err := resolveRuntime(command, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Resolved.Engine != "project.us.example.test:443" || runtime.Resolved.Region != "us-east-1" {
		t.Fatalf("unexpected regional runtime: %#v", runtime.Resolved)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestResolveRuntimeSelectsAuthorizedRegionWithContextToken(t *testing.T) {
	clearRstreamTestEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer context-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(controlplane.Project{
			Endpoint: "project",
			RegionalEndpoints: []controlplane.ProjectRegionalEndpoint{
				{Provider: "aws", Region: "us-east-1", Domain: "us.example.test", EnginePort: 443},
			},
		})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Defaults: config.Defaults{Context: &config.DefaultContext{Name: "global"}},
		Contexts: []config.Context{{
			Name:            "global",
			APIURL:          server.URL,
			Engine:          "project.global.example.test:443",
			ProjectEndpoint: "project",
			Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
				Kind:  config.TokenStorageInline,
				Value: "context-token",
			}}},
		}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	command := runtimeFlagsCommand()
	mustSetFlag(t, command, "region", "us-east-1")
	t.Setenv("RSTREAM_CONFIG", path)
	runtime, err := resolveRuntime(command, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Resolved.Engine != "project.us.example.test:443" {
		t.Fatalf("engine = %q, want regional engine", runtime.Resolved.Engine)
	}
}

func TestResolveControlPlaneIgnoresDefaultContext(t *testing.T) {
	clearRstreamTestEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Defaults: config.Defaults{Context: &config.DefaultContext{Name: "demo"}},
		Environments: []config.Environment{{APIURL: config.DefaultAPIURL(), Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
			Kind:  config.TokenStorageInline,
			Value: "api-token",
		}}}}},
		Contexts: []config.Context{{Name: "demo", APIURL: "https://other.example.com", Engine: "engine.example.com:443"}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	command := runtimeFlagsCommand()
	t.Setenv("RSTREAM_CONFIG", path)
	runtime, err := resolveControlPlane(command, true)
	if err != nil {
		t.Fatalf("resolveControlPlane() error = %v", err)
	}
	if runtime.Resolved.Context != nil || runtime.Resolved.Token != "api-token" {
		t.Fatalf("Control plane API runtime should ignore default context: %#v", runtime.Resolved)
	}
}

func TestResolveControlPlaneHonorsExplicitContext(t *testing.T) {
	clearRstreamTestEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Environments: []config.Environment{{APIURL: "http://localhost:3000", Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
			Kind:  config.TokenStorageInline,
			Value: "local-token",
		}}}}},
		Contexts: []config.Context{{Name: "tests", APIURL: "http://localhost:3000", Engine: "tests.c.localhost.rstream.io:443"}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	command := runtimeFlagsCommand()
	t.Setenv("RSTREAM_CONFIG", path)
	t.Setenv("RSTREAM_CONTEXT", "tests")
	runtime, err := resolveControlPlane(command, true)
	if err != nil {
		t.Fatalf("resolveControlPlane() error = %v", err)
	}
	if runtime.Resolved.Context == nil || runtime.Resolved.Context.Name != "tests" || runtime.Resolved.APIURL != "http://localhost:3000" || runtime.Resolved.Token != "local-token" {
		t.Fatalf("Control plane API runtime should honor explicit context: %#v", runtime.Resolved)
	}
}

func TestResolveControlPlaneTokenPrecedence(t *testing.T) {
	clearRstreamTestEnv(t)
	cfg := config.Config{Environments: []config.Environment{{APIURL: "https://api.example.com", Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
		Kind:  config.TokenStorageInline,
		Value: "config-token",
	}}}}}}
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "env-token")
	token, err := resolveControlPlaneToken(cfg, "https://api.example.com")
	if err != nil || token != "env-token" {
		t.Fatalf("resolveControlPlaneToken(env) = %q, %v", token, err)
	}
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "")
	token, err = resolveControlPlaneToken(cfg, "https://api.example.com")
	if err != nil || token != "config-token" {
		t.Fatalf("resolveControlPlaneToken(config) = %q, %v", token, err)
	}
	token, err = resolveControlPlaneToken(config.Config{}, "https://missing.example.com")
	if err != nil || token != "" {
		t.Fatalf("resolveControlPlaneToken(missing) = %q, %v", token, err)
	}
}

func runtimeFlagsCommand() *cobra.Command {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("config", "", "")
	command.Flags().String("api-url", "", "")
	command.Flags().String("context", "", "")
	command.Flags().String("region", "", "")
	command.Flags().String("tunnel-transport", "", "")
	return command
}
