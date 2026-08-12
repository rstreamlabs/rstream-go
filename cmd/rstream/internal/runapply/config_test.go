// See LICENSE file in the project root for license information.

package runapply

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/config"
	"gopkg.in/yaml.v3"
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

func TestDesiredTunnelsPublishedTCP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `version: 1
tunnels:
  - name: ssh
    forward: "22"
    tunnel:
      protocol: tcp
      port: 10042
      allowCrossRegionRouting: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	desired, err := DesiredTunnels(path, runmodel.ResolvedContext{Engine: "engine", Token: "token"}, nil)
	if err != nil {
		t.Fatalf("DesiredTunnels() error = %v", err)
	}
	props := desired[0].Props
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolTCP || props.Type == nil || *props.Type != rstream.TunnelTypeBytestream {
		t.Fatalf("unexpected TCP properties: %#v", props)
	}
	if props.Publish == nil || !*props.Publish || props.Port == nil || *props.Port != 10042 {
		t.Fatalf("unexpected TCP publication properties: %#v", props)
	}
	if props.AllowCrossRegionRouting == nil || !*props.AllowCrossRegionRouting {
		t.Fatalf("AllowCrossRegionRouting = %#v, want true", props.AllowCrossRegionRouting)
	}
}

func TestDesiredTunnelsRejectsZeroPublishedTCPPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `version: 1
tunnels:
  - name: ssh
    forward: "22"
    tunnel:
      protocol: tcp
      port: 0
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_, err := DesiredTunnels(path, runmodel.ResolvedContext{Engine: "engine", Token: "token"}, nil)
	if err == nil || !strings.Contains(err.Error(), "between 1 and 65535") {
		t.Fatalf("expected TCP port validation error, got %v", err)
	}
}

func TestDesiredTunnelsRejectsPortWithoutTCP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `version: 1
tunnels:
  - name: web
    forward: "8080"
    tunnel:
      protocol: http
      port: 10042
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	_, err := DesiredTunnels(path, runmodel.ResolvedContext{Engine: "engine", Token: "token"}, nil)
	if err == nil || !strings.Contains(err.Error(), `port requires protocol "tcp"`) {
		t.Fatalf("expected TCP port validation error, got %v", err)
	}
}

func TestDesiredTunnelsAcceptsCrossRegionRoutingForHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `version: 1
tunnels:
  - name: web
    forward: "8080"
    tunnel:
      protocol: http
      allowCrossRegionRouting: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	desired, err := DesiredTunnels(path, runmodel.ResolvedContext{Engine: "engine", Token: "token"}, nil)
	if err != nil {
		t.Fatalf("DesiredTunnels() error = %v", err)
	}
	if len(desired) != 1 || desired[0].Props.AllowCrossRegionRouting == nil || !*desired[0].Props.AllowCrossRegionRouting {
		t.Fatalf("unexpected routing properties: %#v", desired)
	}
}

func TestDesiredTunnelsRejectsPublishedTCPProtocolOptions(t *testing.T) {
	t.Parallel()
	for name, option := range map[string]string{
		"hostname":     "host: ssh.example.com",
		"upstream TLS": "upstreamTLS: true",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			yaml := "version: 1\ntunnels:\n  - name: ssh\n    forward: \"22\"\n    tunnel:\n      protocol: tcp\n      " + option + "\n"
			if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
				t.Fatalf("write temp file: %v", err)
			}
			_, err := DesiredTunnels(path, runmodel.ResolvedContext{Engine: "engine", Token: "token"}, nil)
			if err == nil || !strings.Contains(err.Error(), `protocol "tcp" does not accept`) {
				t.Fatalf("expected published TCP option validation error, got %v", err)
			}
		})
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

func TestTunnelPropertiesFromSpecFullSurface(t *testing.T) {
	props, err := tunnelPropertiesFromSpec(&TunnelSpec{
		Publish:     rstream.BoolPtr(false),
		Protocol:    "http",
		Type:        "bytestream",
		Host:        " app.example.com ",
		UpstreamTLS: rstream.BoolPtr(true),
		Labels:      map[string]string{"tier": "edge"},
		TrustedIPs:  []string{"10.0.0.0/8"},
		GeoIP:       []string{"FR"},
		HTTP: &HTTPSpec{
			Version: "h2c",
			Auth:    &HTTPAuthSpec{Token: rstream.BoolPtr(true), Rstream: rstream.BoolPtr(false)},
			Gate:    &HTTPGateSpec{Challenge: rstream.BoolPtr(true)},
		},
		TLS: &TLSSpec{
			Mode:       "terminated",
			MinVersion: "tls1.3",
			ALPNs:      []string{"h2"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if props.Publish == nil || *props.Publish {
		t.Fatalf("publish flag not applied")
	}
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolHTTP {
		t.Fatalf("unexpected protocol: %#v", props.Protocol)
	}
	if props.Type == nil || *props.Type != rstream.TunnelTypeBytestream {
		t.Fatalf("unexpected type: %#v", props.Type)
	}
	if props.Hostname == nil || *props.Hostname != "app.example.com" {
		t.Fatalf("unexpected host: %#v", props.Hostname)
	}
	if props.HTTPUseTLS == nil || !*props.HTTPUseTLS || props.UpstreamTLS == nil || !*props.UpstreamTLS {
		t.Fatalf("expected HTTP upstream TLS propagation")
	}
	if props.HTTPVersion == nil || *props.HTTPVersion != rstream.HTTP2 {
		t.Fatalf("unexpected HTTP version: %#v", props.HTTPVersion)
	}
	if props.TokenAuth == nil || !*props.TokenAuth || props.RstreamAuth == nil || *props.RstreamAuth || props.ChallengeMode == nil || !*props.ChallengeMode {
		t.Fatalf("unexpected auth/gate values: %#v", props)
	}
	if props.TLSMode == nil || *props.TLSMode != rstream.TLSModeTerminated || props.TLSMinVersion == nil || *props.TLSMinVersion != "tls1.3" {
		t.Fatalf("unexpected TLS settings: %#v", props)
	}
}

func TestTunnelPropertiesFromSpecReadsMTLSAuth(t *testing.T) {
	props, err := tunnelPropertiesFromSpec(&TunnelSpec{
		TLS: &TLSSpec{MTLS: rstream.BoolPtr(true)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if props.MTLSAuth == nil || !*props.MTLSAuth {
		t.Fatalf("expected mTLS auth to be enabled, got %#v", props.MTLSAuth)
	}
}

func TestTunnelPropertiesFromSpecMapsDatagramGuaranteedDelivery(t *testing.T) {
	props, err := tunnelPropertiesFromSpec(&TunnelSpec{
		Type:                       "datagram",
		DatagramGuaranteedDelivery: rstream.BoolPtr(true),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if props.DatagramGuaranteedDelivery == nil || !*props.DatagramGuaranteedDelivery {
		t.Fatalf("DatagramGuaranteedDelivery = %#v, want true", props.DatagramGuaranteedDelivery)
	}
}

func TestTunnelPropertiesFromSpecAllowsMultiplePublishedAuthMethods(t *testing.T) {
	props, err := tunnelPropertiesFromSpec(&TunnelSpec{
		HTTP: &HTTPSpec{Auth: &HTTPAuthSpec{Token: rstream.BoolPtr(true)}},
		TLS:  &TLSSpec{MTLS: rstream.BoolPtr(true)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if props.TokenAuth == nil || !*props.TokenAuth || props.MTLSAuth == nil || !*props.MTLSAuth {
		t.Fatalf("expected auth methods to be preserved, got %#v", props)
	}
}

func TestTunnelPropertiesFromSpecRejectsConflictingHTTPUpstreamTLS(t *testing.T) {
	_, err := tunnelPropertiesFromSpec(&TunnelSpec{
		Protocol:    "http",
		UpstreamTLS: rstream.BoolPtr(true),
		HTTP:        &HTTPSpec{UpstreamTLS: rstream.BoolPtr(false)},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected upstream TLS conflict, got %v", err)
	}
}

func TestRunApplyParsers(t *testing.T) {
	protocol, err := parseProtocol(" QUIC ")
	if err != nil || protocol != rstream.ProtocolQUIC {
		t.Fatalf("parseProtocol got %q err=%v", protocol, err)
	}
	protocol, err = parseProtocol(" WebTTY ")
	if err != nil || protocol != rstream.ProtocolWebTTY {
		t.Fatalf("parseProtocol got %q err=%v", protocol, err)
	}
	tunnelType, err := parseTunnelType(" datagram ")
	if err != nil || tunnelType != rstream.TunnelTypeDatagram {
		t.Fatalf("parseTunnelType got %q err=%v", tunnelType, err)
	}
	httpVersion, err := parseHTTPVersion(" HTTP/1.1 ")
	if err != nil || httpVersion != rstream.HTTP1_1 {
		t.Fatalf("parseHTTPVersion got %q err=%v", httpVersion, err)
	}
	tlsMode, err := parseTLSMode(" passthrough ")
	if err != nil || tlsMode != rstream.TLSModePassthrough {
		t.Fatalf("parseTLSMode got %q err=%v", tlsMode, err)
	}
	tlsMinVersion, err := parseTLSMinVersion(" TLS1.2 ")
	if err != nil || tlsMinVersion != "tls1.2" {
		t.Fatalf("parseTLSMinVersion got %q err=%v", tlsMinVersion, err)
	}
	for name, fn := range map[string]func(string) error{
		"protocol": func(v string) error { _, err := parseProtocol(v); return err },
		"type":     func(v string) error { _, err := parseTunnelType(v); return err },
		"http":     func(v string) error { _, err := parseHTTPVersion(v); return err },
		"tlsMode":  func(v string) error { _, err := parseTLSMode(v); return err },
		"tlsMin":   func(v string) error { _, err := parseTLSMinVersion(v); return err },
	} {
		if err := fn("invalid"); err == nil {
			t.Fatalf("%s parser should reject invalid input", name)
		}
	}
}

func TestResolveNamedContextsErrors(t *testing.T) {
	if _, err := resolveNamedContexts(map[string]ContextEntry{" ": {Engine: "engine", Token: "token"}}, nil); err == nil {
		t.Fatalf("expected blank context name error")
	}
	if _, err := resolveNamedContexts(map[string]ContextEntry{"prod": {External: true}}, nil); err == nil {
		t.Fatalf("expected missing external lookup error")
	}
	if _, err := resolveInlineContext("", "token", nil); err == nil {
		t.Fatalf("expected missing engine error")
	}
	if _, err := resolveInlineContext("engine", "", nil); err == nil {
		t.Fatalf("expected missing token error")
	}
	ctx, err := resolveInlineContext(" engine ", " token ", &config.TransportConfig{UseQUIC: rstream.BoolPtr(true)})
	if err != nil {
		t.Fatalf("unexpected inline context error: %v", err)
	}
	if ctx.Engine != "engine" || ctx.Token != "token" || ctx.Transport != nil || ctx.TransportConfig == nil || ctx.TransportConfig.UseQUIC == nil || !*ctx.TransportConfig.UseQUIC {
		t.Fatalf("unexpected inline context: %#v", ctx)
	}
}

func TestContextRefUnmarshalYAMLValidation(t *testing.T) {
	var cfg FileConfig
	err := yaml.Unmarshal([]byte("version: 1\ntunnels:\n- name: x\n  forward: \"8080\"\n  context:\n    unknown: true\n  tunnel:\n    publish: true\n"), &cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown context field error, got %v", err)
	}
	err = yaml.Unmarshal([]byte("version: 1\ntunnels:\n- name: x\n  forward: \"8080\"\n  context: {}\n  tunnel:\n    publish: true\n"), &cfg)
	if err != nil {
		t.Fatalf("empty inline context should decode: %v", err)
	}
	if cfg.Tunnels[0].Context == nil || cfg.Tunnels[0].Context.Inline == nil {
		t.Fatalf("expected inline context, got %#v", cfg.Tunnels[0].Context)
	}
}

func TestTunnelPropertiesFromSpecKeepsSlicesAndMaps(t *testing.T) {
	labels := map[string]string{"a": "b"}
	geo := []string{"FR"}
	trusted := []string{"10.0.0.0/8"}
	props, err := tunnelPropertiesFromSpec(&TunnelSpec{Labels: labels, GeoIP: geo, TrustedIPs: trusted})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(props.Labels, labels) || !reflect.DeepEqual(props.GeoIP, geo) || !reflect.DeepEqual(props.TrustedIPs, trusted) {
		t.Fatalf("collections not applied: %#v", props)
	}
}
