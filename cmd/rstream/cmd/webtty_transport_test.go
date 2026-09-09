// See LICENSE file in the project root for license information.

package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/webtty"
)

func TestWebTTYAdvertisesActualTransport(t *testing.T) {
	for _, transport := range []string{"plain", "websocket", "webtransport"} {
		for _, registered := range []bool{false, true} {
			name := transport + "/lightweight"
			var enrollment *webTTYServerEnrollmentFile
			if registered {
				name = transport + "/persistent"
				enrollment = &webTTYServerEnrollmentFile{ServerID: "audit-server"}
			}
			t.Run(name, func(t *testing.T) {
				cmd := newTestWebTTYServerCommand()
				if err := cmd.Flags().Set("transport", transport); err != nil {
					t.Fatal(err)
				}
				properties := newWebTTYServerTunnelProperties(cmd, enrollment)
				if got := properties.Labels["rstream.webtty.transport"]; got != transport {
					t.Fatalf("transport label = %q, want %q", got, transport)
				}
			})
		}
	}
}

func TestWebTTYClientExecutionPathFollowsResolvedTransport(t *testing.T) {
	server := &webtty.ServerInfo{ExecPath: rstream.StringPtr("/terminal")}
	for _, transport := range []webtty.WebTTYTransport{webtty.WebTTYTransportWebSocket, webtty.WebTTYTransportWebTransport} {
		for _, scheme := range []string{"rstrm", "wss", "https", "wts", "wt", "webtransport"} {
			got, err := resolveWebTTYClientExecURL(scheme+"://shell", transport, "", server)
			if err != nil || got != scheme+"://shell/terminal" {
				t.Fatalf("%s/%s: got %q, %v", transport, scheme, got, err)
			}
			got, err = resolveWebTTYClientExecURL(scheme+"://shell", transport, "/override", server)
			if err != nil || got != scheme+"://shell/override" {
				t.Fatalf("override %s/%s: got %q, %v", transport, scheme, got, err)
			}
		}
	}
	if got, err := resolveWebTTYClientExecURL("rstrm://shell", webtty.WebTTYTransportPlain, "", server); err != nil || got != "rstrm://shell" {
		t.Fatalf("plain: got %q, %v", got, err)
	}
	if _, err := resolveWebTTYClientExecURL("rstrm://shell", webtty.WebTTYTransportPlain, "/override", server); err == nil {
		t.Fatal("plain accepted an HTTP execution path after discovery")
	}
}

func TestWebTTYTransportDiscoveryUsesOnlyEngine(t *testing.T) {
	for _, transport := range []webtty.WebTTYTransport{webtty.WebTTYTransportPlain, webtty.WebTTYTransportWebSocket, webtty.WebTTYTransportWebTransport} {
		t.Run(string(transport), func(t *testing.T) {
			var engineCalls, controlCalls atomic.Int32
			engine := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				engineCalls.Add(1)
				if r.URL.Path != "/api/tunnels" || r.Method != http.MethodGet {
					http.Error(w, "unexpected engine request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(rstream.ListTunnelsResponse{{TunnelProperties: rstream.TunnelProperties{ID: rstream.StringPtr("shell"), Labels: map[string]string{webtty.WebTTYApplicationProtocolKey: webtty.WebTTYApplicationProtocol, webtty.WebTTYTransportLabelKey: string(transport)}}, Status: "online"}})
			}))
			defer engine.Close()
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				controlCalls.Add(1)
				http.Error(w, "restricted token cannot use control plane", http.StatusForbidden)
			}))
			defer control.Close()
			roots := x509.NewCertPool()
			roots.AddCert(engine.Certificate())
			engineURL := strings.TrimPrefix(engine.URL, "https://")
			client := &rstream.Client{EngineURL: &engineURL, Token: rstream.StringPtr("fixture-token"), TLSClientConfig: &tls.Config{RootCAs: roots}}
			defer client.Close()
			runtime := &resolvedRuntime{Resolved: config.Resolved{APIURL: control.URL, Context: &config.Context{ProjectEndpoint: "project"}}}
			resolution, err := resolveWebTTYClientRstream(t.Context(), runtime, client, "rstrm://shell")
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolution.Server.ResolveTransport("")
			if err != nil || got != transport {
				t.Fatalf("transport = %q, %v; want %q", got, err, transport)
			}
			if engineCalls.Load() != 1 || controlCalls.Load() != 0 {
				t.Fatalf("calls: engine=%d control=%d; want 1 and 0", engineCalls.Load(), controlCalls.Load())
			}
		})
	}
}

func TestWebTTYWithoutDiscoveryDoesNotRequireMetadataClients(t *testing.T) {
	resolution, err := resolveWebTTYClientRstreamWithDiscovery(t.Context(), nil, nil, "rstrm://restricted-device", true)
	if err != nil || resolution.URL != "rstrm://restricted-device" || resolution.Server != nil || resolution.RuntimeE2E != nil {
		t.Fatalf("explicit data-plane resolution = %#v, %v", resolution, err)
	}
	cmd := newTestWebTTYClientCommand()
	if err := cmd.Flags().Set("no-discovery", "true"); err != nil {
		t.Fatal(err)
	}
	err = runWebTTYClientWithOptions(cmd, "rstrm://restricted-device", nil, webTTYClientRunOptions{})
	if err == nil || !strings.Contains(err.Error(), "requires --transport") {
		t.Fatalf("expected explicit transport requirement, got %v", err)
	}
}

func TestUIWebTTYTransportConfiguration(t *testing.T) {
	clearRstreamTestEnv(t)
	for _, transport := range []webtty.WebTTYTransport{webtty.WebTTYTransportPlain, webtty.WebTTYTransportWebSocket, webtty.WebTTYTransportWebTransport} {
		t.Run(string(transport), func(t *testing.T) {
			app := &uiApp{}
			plan, selection, err := app.webTTYSessionConfig(t.Context(), webtty.ServerInfo{Target: "shell", RstreamURL: "rstrm://shell", Transport: &transport}, slog.Default(), "", false)
			if err != nil || selection != nil {
				t.Fatalf("session config: %v, %v", selection, err)
			}
			if plan.config.Transport != transport || plan.config.DialContext == nil || plan.config.DialPacketContext == nil {
				t.Fatal("UI lost resolved transport or tunnel dialers")
			}
			if (plan.config.TLSConfig != nil) != (transport == webtty.WebTTYTransportWebTransport) {
				t.Fatal("internal QUIC TLS configuration applied to wrong transport")
			}
		})
	}
}

func TestMCPWebTTYExplicitTransport(t *testing.T) {
	clearRstreamTestEnv(t)
	for _, transport := range []string{"plain", "websocket", "webtransport", "future", ""} {
		t.Run(transport, func(t *testing.T) {
			encoded, err := json.Marshal(transport)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := mcpWebTTYExecClientConfig(t.Context(), map[string]json.RawMessage{"url": json.RawMessage(`"ws://localhost"`), "transport": encoded, "no_discovery": json.RawMessage(`true`)}, []string{"true"})
			if transport == "" || transport == "future" {
				if err == nil {
					t.Fatal("accepted invalid explicit connection configuration")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer cfg.Close()
			if string(cfg.Transport) != transport {
				t.Fatalf("transport = %q, want %q", cfg.Transport, transport)
			}
		})
	}
}

func TestWebTTYDataDirectoryIsExplicitAndAbsolute(t *testing.T) {
	root := filepath.Join(t.TempDir(), "device-state")
	t.Setenv("RSTREAM_DATA_DIR", root)
	got, err := defaultWebTTYServerEnrollmentPath("shell")
	if err != nil || got != filepath.Join(root, "webtty", "enrollments", "shell.yaml") {
		t.Fatalf("enrollment = %q, %v", got, err)
	}
	t.Setenv("RSTREAM_DATA_DIR", "relative-state")
	if _, err := defaultRstreamHomeDir(); err == nil {
		t.Fatal("accepted relative state directory")
	}
}

func TestWebTTYNoDiscoveryRejectsRegionBeforeControlPlaneIO(t *testing.T) {
	clearRstreamTestEnv(t)
	var calls atomic.Int32
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "control plane forbidden", http.StatusForbidden)
	}))
	defer control.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Defaults: config.Defaults{Context: &config.DefaultContext{Name: "device"}},
		Contexts: []config.Context{{Name: "device", APIURL: control.URL, Engine: "project.engine.example.test:443", ProjectEndpoint: "project", Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{Kind: config.TokenStorageInline, Value: "restricted-token"}}}}},
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RSTREAM_CONFIG", path)
	command := runtimeFlagsCommand(t)
	runtime, err := resolveRuntimeWithRegionDiscovery(command, true, true, false)
	if err != nil || runtime.Resolved.Engine != "project.engine.example.test:443" {
		t.Fatalf("explicit engine resolution: %v", err)
	}
	mustSetFlag(t, command, "region", "us-east-1")
	if _, err := resolveRuntimeWithRegionDiscovery(command, true, true, false); err == nil || !strings.Contains(err.Error(), "without a region selector") {
		t.Fatalf("region error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("control plane calls = %d", calls.Load())
	}
}
