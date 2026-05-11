// See LICENSE file in the project root for license information.

package config

import (
	"path/filepath"
	"testing"

	"github.com/rstreamlabs/rstream-go"
)

func TestResolveFromEnvLoadsConfiguredFileAndAppliesEnvOverrides(t *testing.T) {
	clearRstreamEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteAtomic(path, Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "dev"}},
		Contexts: []Context{{
			Name:   "dev",
			Engine: "file-engine.example.com:443",
			Auth:   &Auth{Token: &Token{Storage: &TokenStorage{Kind: TokenStorageInline, Value: "file-token"}}},
		}},
	}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", path)
	t.Setenv("RSTREAM_ENGINE", "env-engine.example.com:443")
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "env-token")
	resolution, err := ResolveFromEnv(ClientEnvOptions{RequireEngine: true, RequireToken: true})
	if err != nil {
		t.Fatalf("ResolveFromEnv() error = %v", err)
	}
	if resolution.ConfigPath != path {
		t.Fatalf("ConfigPath = %q, want %q", resolution.ConfigPath, path)
	}
	if resolution.Resolved.ContextName != "dev" {
		t.Fatalf("ContextName = %q, want dev", resolution.Resolved.ContextName)
	}
	if resolution.Resolved.Engine != "env-engine.example.com:443" {
		t.Fatalf("Engine = %q, want env override", resolution.Resolved.Engine)
	}
	if resolution.Resolved.Token != "env-token" {
		t.Fatalf("Token = %q, want env override", resolution.Resolved.Token)
	}
}

func TestNewClientFromResolvedPropagatesTransportAndNoToken(t *testing.T) {
	transport := &rstream.Transport{}
	client, err := NewClientFromResolved(Resolved{
		Engine:    "engine.example.com:443",
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("NewClientFromResolved() error = %v", err)
	}
	if client.EngineURL == nil || *client.EngineURL != "engine.example.com:443" {
		t.Fatalf("EngineURL = %#v, want engine.example.com:443", client.EngineURL)
	}
	if client.Transport != transport {
		t.Fatalf("Transport = %#v, want original transport", client.Transport)
	}
	if client.NoToken == nil || !*client.NoToken {
		t.Fatalf("NoToken = %#v, want true", client.NoToken)
	}
}

func TestNewClientFromEnvOptionsCanSelectQUICTransport(t *testing.T) {
	clearRstreamEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteAtomic(path, Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "dev"}},
		Contexts: []Context{{
			Name:   "dev",
			Engine: "engine.example.com:443",
		}},
	}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", path)
	t.Setenv("RSTREAM_QUIC_TRANSPORT", "1")
	client, err := NewClientFromEnvOptions(ClientEnvOptions{RequireEngine: true})
	if err != nil {
		t.Fatalf("NewClientFromEnvOptions() error = %v", err)
	}
	if _, ok := client.Transport.(*rstream.QUICTransport); !ok {
		t.Fatalf("Transport = %T, want *rstream.QUICTransport", client.Transport)
	}
}

func TestNewClientFromEnvUsesDefaultEnvironmentResolution(t *testing.T) {
	clearRstreamEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteAtomic(path, Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "dev"}},
		Contexts: []Context{{
			Name:   "dev",
			Engine: "env-client.example.com:443",
		}},
	}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", path)
	client, err := NewClientFromEnv()
	if err != nil {
		t.Fatalf("NewClientFromEnv() error = %v", err)
	}
	if client.EngineURL == nil || *client.EngineURL != "env-client.example.com:443" {
		t.Fatalf("EngineURL = %#v, want env-client.example.com:443", client.EngineURL)
	}
	if client.NoToken == nil || !*client.NoToken {
		t.Fatalf("NoToken = %#v, want true", client.NoToken)
	}
}
