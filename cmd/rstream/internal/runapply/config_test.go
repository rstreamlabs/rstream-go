// See LICENSE file in the project root for license information.

package runapply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

func TestLoadConfigValidationAndEnvExpansion(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		setEnv   map[string]string
		wantErr  bool
		assertFn func(t *testing.T, cfg FileConfig)
	}{
		{
			name: "env expansion",
			yaml: `version: 1
tunnels:
  - name: "web"
    forward: "8080"
    tunnel:
      publish: true
      protocol: "http"
contexts:
  main:
    engine: "engine.rstream.io:443"
    token: "${TEST_TOKEN}"
`,
			setEnv: map[string]string{"TEST_TOKEN": "secret"},
			assertFn: func(t *testing.T, cfg FileConfig) {
				ctx := cfg.Contexts["main"]
				if ctx.Token != "secret" {
					t.Fatalf("expected expanded token, got %q", ctx.Token)
				}
			},
		},
		{
			name:    "unknown field",
			yaml:    "version: 1\ntunnels:\n  - nameEname: bad\n    forward: \"8080\"\n    tunnel:\n      publish: true\n",
			wantErr: true,
		},
		{
			name:    "unsupported version",
			yaml:    "version: 2\ntunnels:\n  - name: web\n    forward: \"8080\"\n    tunnel:\n      publish: true\n",
			wantErr: true,
		},
		{
			name:    "missing tunnels",
			yaml:    "version: 1\n",
			wantErr: true,
		},
		{
			name: "legacy auth block is rejected",
			yaml: `version: 1
tunnels:
  - name: "web"
    forward: "8080"
    tunnel:
      publish: true
      protocol: "http"
      auth:
        token: true
`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			for k, v := range tc.setEnv {
				t.Setenv(k, v)
			}
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatalf("write temp file: %v", err)
			}
			cfg, err := LoadConfig(path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.assertFn != nil {
				tc.assertFn(t, cfg)
			}
		})
	}
}

func TestDesiredTunnelsHTTPAuthAndGate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `version: 1
tunnels:
  - name: "web"
    forward: "8080"
    tunnel:
      publish: true
      protocol: "http"
      http:
        upstreamTLS: false
        version: "http/1.1"
        auth:
          token: true
          rstream: false
        gate:
          challenge: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	desired, err := DesiredTunnels(path, runmodel.ResolvedContext{Engine: "engine", Token: "token"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("expected 1 tunnel, got %d", len(desired))
	}
	props := desired[0].Props
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolHTTP {
		t.Fatalf("expected protocol http")
	}
	if props.TokenAuth == nil || *props.TokenAuth != true {
		t.Fatalf("expected token auth enabled")
	}
	if props.RstreamAuth == nil || *props.RstreamAuth != false {
		t.Fatalf("expected rstream auth disabled")
	}
	if props.ChallengeMode == nil || *props.ChallengeMode != true {
		t.Fatalf("expected challenge mode enabled")
	}
}

func TestDesiredTunnelsRejectsHTTPSettingsOnNonHTTPProtocol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `version: 1
tunnels:
  - name: "secure"
    forward: "8080"
    tunnel:
      protocol: "tls"
      http:
        auth:
          token: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_, err := DesiredTunnels(path, runmodel.ResolvedContext{Engine: "engine", Token: "token"}, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), `http settings require protocol "http"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContextResolutionOrder(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		fallback runmodel.ResolvedContext
		lookup   ResolvedContextLookup
		want     map[string]string
		wantErr  bool
	}{
		{
			name: "inline-named-fallback",
			yaml: `version: 1
tunnels:
  - name: inline
    forward: "8080"
    context:
      engine: "inline-engine"
      token: "inline-token"
    tunnel:
      publish: true
  - name: named
    forward: "8081"
    context: "named"
    tunnel:
      publish: true
  - name: fallback
    forward: "8082"
    tunnel:
      publish: true
contexts:
  named:
    engine: "named-engine"
    token: "named-token"
`,
			fallback: runmodel.ResolvedContext{Engine: "fallback-engine", Token: "fallback-token"},
			want: map[string]string{
				"inline":   "inline-engine",
				"named":    "named-engine",
				"fallback": "fallback-engine",
			},
		},
		{
			name: "external-context",
			yaml: `version: 1
tunnels:
  - name: ext
    forward: "8080"
    context: "ext"
    tunnel:
      publish: true
contexts:
  ext:
    external: true
    name: "prod"
`,
			fallback: runmodel.ResolvedContext{Engine: "fallback-engine", Token: "fallback-token"},
			lookup: func(name string) (runmodel.ResolvedContext, error) {
				return runmodel.ResolvedContext{Engine: "external-engine", Token: "external-token", Name: name}, nil
			},
			want: map[string]string{"ext": "external-engine"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatalf("write temp file: %v", err)
			}
			desired, err := DesiredTunnels(path, tc.fallback, tc.lookup)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(desired) != len(tc.want) {
				t.Fatalf("expected %d tunnels, got %d", len(tc.want), len(desired))
			}
			for _, d := range desired {
				engine, ok := tc.want[d.Name]
				if !ok {
					t.Fatalf("unexpected tunnel %q", d.Name)
				}
				if d.Context.Engine != engine {
					t.Fatalf("tunnel %q engine mismatch: got %q want %q", d.Name, d.Context.Engine, engine)
				}
			}
		})
	}
}
