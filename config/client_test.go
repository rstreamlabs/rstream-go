// See LICENSE file in the project root for license information.

package config

import (
	"crypto/tls"
	"path/filepath"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/controlplane"
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
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{{1}}}}}
	client, err := NewClientFromResolved(Resolved{
		Engine:          "engine.example.com:443",
		Transport:       transport,
		TLSClientConfig: tlsCfg,
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
	if client.TLSClientConfig != tlsCfg {
		t.Fatalf("TLSClientConfig = %#v, want original config", client.TLSClientConfig)
	}
	if client.NoToken == nil || !*client.NoToken {
		t.Fatalf("NoToken = %#v, want true", client.NoToken)
	}
}

func TestResolveProjectRegionKeepsGlobalStableDomainEndpoint(t *testing.T) {
	resolved := Resolved{Region: "eu-west-3"}
	project := controlplane.Project{
		Endpoint:   "project",
		Domain:     "global.example.test",
		EnginePort: 443,
		RegionalEndpoints: []controlplane.ProjectRegionalEndpoint{{
			Region:     "eu-west-3",
			Domain:     "eu.example.test",
			EnginePort: 8443,
		}},
	}
	if err := ResolveProjectRegion(&resolved, project); err != nil {
		t.Fatalf("ResolveProjectRegion() error = %v", err)
	}
	if resolved.Engine != "project.eu.example.test:8443" {
		t.Fatalf("Engine = %q, want regional endpoint", resolved.Engine)
	}
	if resolved.StableDomainEndpoint() != "project.global.example.test:443" {
		t.Fatalf("StableDomainEndpoint() = %q, want global endpoint", resolved.StableDomainEndpoint())
	}
}

func TestNewClientFromEnvOptionsCanSelectQUICTransport(t *testing.T) {
	clearRstreamEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteAtomic(path, Config{
		Defaults: Defaults{Context: &DefaultContext{Name: "dev"}},
		Contexts: []Context{{
			Name:      "dev",
			Engine:    "engine.example.com:443",
			Transport: &TransportConfig{Proxy: &ProxyConfig{SOCKS5: "socks5://proxy.example.com:1080"}},
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
	transport, ok := client.Transport.(*rstream.QUICTransport)
	if !ok {
		t.Fatalf("Transport = %T, want *rstream.QUICTransport", client.Transport)
	}
	if transport.ProxySOCKS5 == nil || *transport.ProxySOCKS5 != "socks5://proxy.example.com:1080" {
		t.Fatalf("QUIC transport did not preserve proxy settings: %#v", transport)
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
	if _, ok := client.Transport.(*rstream.AutoTransport); !ok {
		t.Fatalf("Transport = %T, want default AutoTransport", client.Transport)
	}
}

func TestTunnelTransportEnvironmentPrecedence(t *testing.T) {
	clearRstreamEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteAtomic(path, Config{Defaults: Defaults{Context: &DefaultContext{Name: "dev"}}, Contexts: []Context{{Name: "dev", Engine: "engine.example.com:443", Transport: &TransportConfig{Mode: "quic"}}}}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", path)
	t.Setenv("RSTREAM_QUIC_TRANSPORT", "1")
	t.Setenv("RSTREAM_TUNNEL_TRANSPORT", "tls")
	client, err := NewClientFromEnvOptions(ClientEnvOptions{RequireEngine: true})
	if err != nil {
		t.Fatalf("NewClientFromEnvOptions() error = %v", err)
	}
	if _, ok := client.Transport.(*rstream.Transport); !ok {
		t.Fatalf("Transport = %T, want canonical TLS override", client.Transport)
	}
	_, err = NewClientFromEnvOptions(ClientEnvOptions{RequireEngine: true, TunnelTransport: "invalid"})
	if err == nil {
		t.Fatal("expected invalid explicit tunnel transport error")
	}
}
