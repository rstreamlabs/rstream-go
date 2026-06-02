// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/spf13/cobra"
)

func TestTransportFromFlags(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	cfg, err := transportFromFlags(command)
	if err != nil {
		t.Fatalf("transportFromFlags() error = %v", err)
	}
	if cfg != nil {
		t.Fatalf("transportFromFlags() with no flags = %#v, want nil", cfg)
	}
	mustSetFlag(t, command, "bind-interface", "lo0")
	mustSetFlag(t, command, "ip-family", "ipv6")
	mustSetFlag(t, command, "dns-override", "1.1.1.1:853")
	mustSetFlag(t, command, "mptcp", "true")
	mustSetFlag(t, command, "engine-tls-ca-file", "/tmp/engine-ca.pem")
	mustSetFlag(t, command, "engine-tls-server-name", "engine.local")
	mustSetFlag(t, command, "proxy-http", "http://proxy.local:3128")
	mustSetFlag(t, command, "proxy-username", "user")
	mustSetFlag(t, command, "proxy-password", "pass")
	mustSetFlag(t, command, "proxy-http-header", "X-Trace=abc")
	mustSetFlag(t, command, "proxy-tls-ca-file", "/tmp/proxy-ca.pem")
	mustSetFlag(t, command, "proxy-tls-server-name", "proxy.local")
	mustSetFlag(t, command, "proxy-from-environment", "true")
	cfg, err = transportFromFlags(command)
	if err != nil {
		t.Fatalf("transportFromFlags() error = %v", err)
	}
	if cfg.Bind == nil || cfg.Bind.Mode != "interface" || cfg.Bind.Interface != "lo0" {
		t.Fatalf("bind flags not parsed: %#v", cfg.Bind)
	}
	if cfg.IPFamily != "ipv6" || cfg.DNS == nil || cfg.DNS.Override != "1.1.1.1:853" {
		t.Fatalf("network flags not parsed: %#v", cfg)
	}
	if cfg.MPTCP == nil || !*cfg.MPTCP {
		t.Fatalf("mptcp flag not parsed")
	}
	if cfg.TLS == nil || cfg.TLS.CAFile != "/tmp/engine-ca.pem" || cfg.TLS.ServerName != "engine.local" {
		t.Fatalf("engine TLS flags not parsed: %#v", cfg.TLS)
	}
	if cfg.Proxy == nil || cfg.Proxy.HTTP != "http://proxy.local:3128" || cfg.Proxy.Username != "user" || cfg.Proxy.Password != "pass" {
		t.Fatalf("proxy flags not parsed: %#v", cfg.Proxy)
	}
	if cfg.Proxy.Headers["X-Trace"] != "abc" {
		t.Fatalf("proxy headers not parsed: %#v", cfg.Proxy.Headers)
	}
	if cfg.Proxy.TLS == nil || cfg.Proxy.TLS.CAFile != "/tmp/proxy-ca.pem" || cfg.Proxy.TLS.ServerName != "proxy.local" {
		t.Fatalf("proxy TLS flags not parsed: %#v", cfg.Proxy.TLS)
	}
	if cfg.Proxy.FromEnvironment == nil || !*cfg.Proxy.FromEnvironment {
		t.Fatalf("proxy fromEnvironment flag not parsed: %#v", cfg.Proxy)
	}
	command = &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	mustSetFlag(t, command, "proxy-socks5", "socks5://socks.local:1080")
	cfg, err = transportFromFlags(command)
	if err != nil {
		t.Fatalf("transportFromFlags(SOCKS5) error = %v", err)
	}
	if cfg.Proxy == nil || cfg.Proxy.SOCKS5 != "socks5://socks.local:1080" {
		t.Fatalf("SOCKS5 proxy flag not parsed: %#v", cfg.Proxy)
	}
	mustSetFlag(t, command, "proxy-http-header", "X-Trace=abc")
	_, err = transportFromFlags(command)
	if err == nil || !strings.Contains(err.Error(), "--proxy-http-header") {
		t.Fatalf("expected SOCKS5/header validation error, got %v", err)
	}
	command = &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	mustSetFlag(t, command, "proxy-socks5", "socks5://socks.local:1080")
	mustSetFlag(t, command, "proxy-tls-ca-file", "/tmp/proxy-ca.pem")
	_, err = transportFromFlags(command)
	if err == nil || !strings.Contains(err.Error(), "--proxy-tls-*") {
		t.Fatalf("expected SOCKS5/TLS validation error, got %v", err)
	}
	command = &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	mustSetFlag(t, command, "proxy-tls-ca-file", "/tmp/proxy-ca.pem")
	_, err = transportFromFlags(command)
	if err == nil || !strings.Contains(err.Error(), "--proxy-tls-*") {
		t.Fatalf("expected standalone proxy TLS validation error, got %v", err)
	}
}

func TestResolveContextSelection(t *testing.T) {
	command := contextSelectionCommand()
	mustSetFlag(t, command, "api-url", "https://api.example.com")
	selection, err := resolveContextSelection(command, config.Config{})
	if err != nil {
		t.Fatalf("resolveContextSelection() error = %v", err)
	}
	if !selection.useAPIURL || selection.apiURL != "https://api.example.com" || selection.unlinked {
		t.Fatalf("unexpected selection: %#v", selection)
	}
	command = contextSelectionCommand()
	mustSetFlag(t, command, "api-url", "")
	selection, err = resolveContextSelection(command, config.Config{})
	if err != nil {
		t.Fatalf("resolveContextSelection() error = %v", err)
	}
	if !selection.unlinked || selection.useAPIURL {
		t.Fatalf("empty --api-url should select unlinked context: %#v", selection)
	}
	command = contextSelectionCommand()
	mustSetFlag(t, command, "api-url", "https://api.example.com")
	mustSetFlag(t, command, "no-api-url", "true")
	if _, err := resolveContextSelection(command, config.Config{}); err == nil || !strings.Contains(err.Error(), "cannot use --api-url") {
		t.Fatalf("expected conflicting API URL flags error, got %v", err)
	}
	t.Setenv("RSTREAM_API_URL", " https://env.example.com ")
	command = contextSelectionCommand()
	selection, err = resolveContextSelection(command, config.Config{})
	if err != nil {
		t.Fatalf("resolveContextSelection() from env error = %v", err)
	}
	if !selection.useAPIURL || selection.apiURL != "https://env.example.com" {
		t.Fatalf("env API URL not selected: %#v", selection)
	}
}

func TestSelectContext(t *testing.T) {
	cfg := config.Config{Contexts: []config.Context{
		{Name: "prod", APIURL: "https://api.example.com", Engine: "engine-api"},
		{Name: "prod", Engine: "engine-local"},
	}}
	command := contextSelectionCommand()
	mustSetFlag(t, command, "api-url", "https://api.example.com")
	ctx, idx, err := selectContext(command, cfg, "prod")
	if err != nil || idx != 0 || ctx.Engine != "engine-api" {
		t.Fatalf("select API-linked context = %#v, %d, %v", ctx, idx, err)
	}
	command = contextSelectionCommand()
	mustSetFlag(t, command, "no-api-url", "true")
	ctx, idx, err = selectContext(command, cfg, "prod")
	if err != nil || idx != 1 || ctx.Engine != "engine-local" {
		t.Fatalf("select unlinked context = %#v, %d, %v", ctx, idx, err)
	}
	command = contextSelectionCommand()
	if _, _, err := selectContext(command, cfg, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing context error, got %v", err)
	}
}

func TestSetAndRedactContextToken(t *testing.T) {
	ctx := config.Context{Name: "prod", Engine: "engine.example.com:443"}
	setContextToken(&ctx, "secret-token")
	if ctx.Auth == nil || ctx.Auth.Token == nil || ctx.Auth.Token.Storage == nil {
		t.Fatalf("token storage was not initialized: %#v", ctx.Auth)
	}
	if ctx.Auth.Token.Storage.Value != "secret-token" {
		t.Fatalf("token value not stored inline: %#v", ctx.Auth.Token.Storage)
	}
	redacted := redactContext(ctx)
	if redacted.Auth != nil {
		t.Fatalf("redacted context still contains auth: %#v", redacted.Auth)
	}
	if redacted.Name != "prod" || redacted.Engine != "engine.example.com:443" {
		t.Fatalf("redaction changed non-secret fields: %#v", redacted)
	}
	if ctx.Auth == nil || ctx.Auth.Token == nil {
		t.Fatalf("redactContext should not mutate original context auth")
	}
}

func TestContextTokenStorageFromFlags(t *testing.T) {
	command := &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	storage, err := contextTokenStorageFromFlags(command, "prod", "https://api.example.com")
	if err != nil {
		t.Fatalf("contextTokenStorageFromFlags(inline) error = %v", err)
	}
	if storage.Kind != config.TokenStorageInline {
		t.Fatalf("inline storage = %#v", storage)
	}
	command = &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	mustSetFlag(t, command, "token-storage", tokenStorageMacOSKeychain)
	storage, err = contextTokenStorageFromFlags(command, "prod", "https://api.example.com")
	if err != nil {
		t.Fatalf("contextTokenStorageFromFlags(keychain) error = %v", err)
	}
	if storage.Kind != config.TokenStorageKeychain ||
		storage.Provider != config.CredentialProviderMacOS ||
		storage.Service != config.DefaultMacOSKeychainTokenService ||
		storage.Account != "context:https://api.example.com:prod" {
		t.Fatalf("keychain storage = %#v", storage)
	}
	command = &cobra.Command{Use: "test"}
	addContextTransportFlags(command)
	mustSetFlag(t, command, "token-storage", "vault")
	if _, err := contextTokenStorageFromFlags(command, "prod", ""); err == nil || !strings.Contains(err.Error(), "invalid --token-storage") {
		t.Fatalf("expected invalid token storage error, got %v", err)
	}
}

func TestRedactContextRedactsTransportProxySecrets(t *testing.T) {
	ctx := config.Context{
		Name: "prod",
		Transport: &config.TransportConfig{
			Proxy: &config.ProxyConfig{
				HTTP:     "http://proxy.local:3128",
				Username: "proxy-user",
				Password: "proxy-pass",
				Headers: map[string]string{
					"Authorization": "Bearer token",
					"Cookie":        "session=secret",
					"X-Auth-Key":    "auth-key",
					"X-Token":       "token",
					"X-Trace":       "trace-id",
				},
			},
		},
	}
	redacted := redactContext(ctx)
	if redacted.Transport == nil || redacted.Transport.Proxy == nil {
		t.Fatalf("redacted transport proxy missing: %#v", redacted.Transport)
	}
	if redacted.Transport.Proxy.Password != redactedValue {
		t.Fatalf("proxy password was not redacted: %#v", redacted.Transport.Proxy)
	}
	if redacted.Transport.Proxy.Headers["Authorization"] != redactedValue || redacted.Transport.Proxy.Headers["Cookie"] != redactedValue || redacted.Transport.Proxy.Headers["X-Auth-Key"] != redactedValue || redacted.Transport.Proxy.Headers["X-Token"] != redactedValue {
		t.Fatalf("sensitive proxy headers were not redacted: %#v", redacted.Transport.Proxy.Headers)
	}
	if redacted.Transport.Proxy.Headers["X-Trace"] != "trace-id" {
		t.Fatalf("non-sensitive proxy header was changed: %#v", redacted.Transport.Proxy.Headers)
	}
	if ctx.Transport.Proxy.Password != "proxy-pass" || ctx.Transport.Proxy.Headers["Authorization"] != "Bearer token" {
		t.Fatalf("redactContext mutated original context: %#v", ctx.Transport.Proxy)
	}
}

func contextSelectionCommand() *cobra.Command {
	command := &cobra.Command{Use: "test"}
	command.Flags().String("api-url", "", "")
	command.Flags().Bool("no-api-url", false, "")
	return command
}
