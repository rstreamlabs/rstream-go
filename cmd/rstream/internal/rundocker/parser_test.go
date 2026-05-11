// See LICENSE file in the project root for license information.

package rundocker

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
)

func TestParseDesiredTunnels(t *testing.T) {
	cases := []struct {
		name       string
		info       ContainerInfo
		network    string
		wantHost   string
		wantPort   string
		wantLabels map[string]string
		wantErr    bool
	}{
		{
			name: "bare port with network",
			info: ContainerInfo{
				ID:   "abc",
				Name: "web",
				Labels: map[string]string{
					"rstream.tunnel.app.forward":      "8080",
					"rstream.tunnel.app.publish":      "false",
					"rstream.tunnel.app.protocol":     "http",
					"rstream.tunnel.app.http.version": "http/1.1",
					"rstream.tunnel.app.label.env":    "prod",
				},
				Networks: map[string]string{"backend": "10.0.0.2"},
			},
			network:  "backend",
			wantHost: "10.0.0.2",
			wantPort: "8080",
			wantLabels: map[string]string{
				"env":                   "prod",
				runmodel.ManagedByLabel: "run",
				runmodel.SourceLabel:    "docker",
			},
		},
		{
			name: "explicit container host",
			info: ContainerInfo{
				ID:   "abc",
				Name: "web",
				Labels: map[string]string{
					"rstream.tunnel.app.forward": "10.0.0.2:9090",
				},
				Networks: map[string]string{"default": "10.0.0.2"},
			},
			network:  "default",
			wantHost: "10.0.0.2",
			wantPort: "9090",
		},
		{
			name: "missing forward",
			info: ContainerInfo{
				ID:   "abc",
				Name: "web",
				Labels: map[string]string{
					"rstream.tunnel.app.publish": "true",
				},
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desired, err := ParseDesiredTunnels(tc.info, tc.network, runmodel.ResolvedContext{Engine: "engine", Token: "token"})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(desired) != 1 {
				t.Fatalf("expected 1 tunnel, got %d", len(desired))
			}
			if desired[0].Forward.Host != tc.wantHost {
				t.Fatalf("expected host %q, got %q", tc.wantHost, desired[0].Forward.Host)
			}
			if desired[0].Forward.Port != tc.wantPort {
				t.Fatalf("expected port %q, got %q", tc.wantPort, desired[0].Forward.Port)
			}
			if tc.wantLabels != nil {
				for k, v := range tc.wantLabels {
					if desired[0].Props.Labels[k] != v {
						t.Fatalf("expected label %q=%q, got %q", k, v, desired[0].Props.Labels[k])
					}
				}
			}
		})
	}
}

func TestTLSCACertFileDockerLabelIsRejected(t *testing.T) {
	info := ContainerInfo{
		ID:   "abc",
		Name: "web",
		Labels: map[string]string{
			"rstream.tunnel.app.forward":            "8080",
			"rstream.tunnel.app.tls.mtls":           "true",
			"rstream.tunnel.app.tls.mtlsCACertFile": filepath.Join(t.TempDir(), "ca.pem"),
		},
		Networks: map[string]string{"default": "10.0.0.2"},
	}
	_, err := ParseDesiredTunnels(info, "default", runmodel.ResolvedContext{Engine: "engine", Token: "token"})
	if err == nil || !strings.Contains(err.Error(), "mtlsCACertFile is not supported") {
		t.Fatalf("ParseDesiredTunnels() error = %v, want mtlsCACertFile rejection", err)
	}
}

func TestParseDesiredTunnelsTLSDatagramAndAccessLabels(t *testing.T) {
	info := ContainerInfo{
		ID:   "abc",
		Name: "edge",
		Labels: map[string]string{
			"rstream.tunnel.metrics.forward":        "8443",
			"rstream.tunnel.metrics.protocol":       "tls",
			"rstream.tunnel.metrics.type":           "datagram",
			"rstream.tunnel.metrics.publish":        "false",
			"rstream.tunnel.metrics.host":           "metrics.example.com",
			"rstream.tunnel.metrics.upstream-tls":   "true",
			"rstream.tunnel.metrics.trusted-ips":    "10.0.0.0/8, 192.0.2.0/24",
			"rstream.tunnel.metrics.geoip":          "FR, US",
			"rstream.tunnel.metrics.tls.mode":       "passthrough",
			"rstream.tunnel.metrics.tls.minVersion": "tls1.2",
			"rstream.tunnel.metrics.tls.alpns":      "h2, http/1.1",
		},
		Networks: map[string]string{"backend": "10.0.0.8"},
	}
	desired, err := ParseDesiredTunnels(info, "backend", runmodel.ResolvedContext{Engine: "engine", Token: "token"})
	if err != nil {
		t.Fatalf("ParseDesiredTunnels() error = %v", err)
	}
	if len(desired) != 1 {
		t.Fatalf("desired count = %d, want 1", len(desired))
	}
	props := desired[0].Props
	if desired[0].Name != "edge-metrics" || desired[0].Forward.Host != "10.0.0.8" || desired[0].Forward.Port != "8443" {
		t.Fatalf("unexpected desired tunnel: %#v", desired[0])
	}
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolTLS {
		t.Fatalf("Protocol = %#v, want tls", props.Protocol)
	}
	if props.Type == nil || *props.Type != rstream.TunnelTypeDatagram {
		t.Fatalf("Type = %#v, want datagram", props.Type)
	}
	if props.Publish == nil || *props.Publish || props.UpstreamTLS == nil || !*props.UpstreamTLS {
		t.Fatalf("publish/upstream TLS flags not parsed: %#v", props)
	}
	if props.Hostname == nil || *props.Hostname != "metrics.example.com" {
		t.Fatalf("Hostname = %#v, want metrics.example.com", props.Hostname)
	}
	if props.TLSMode == nil || *props.TLSMode != rstream.TLSModePassthrough || props.TLSMinVersion == nil || *props.TLSMinVersion != "tls1.2" {
		t.Fatalf("TLS options not parsed: %#v", props)
	}
	if !reflect.DeepEqual(props.TLSALPNs, []string{"h2", "http/1.1"}) || !reflect.DeepEqual(props.TrustedIPs, []string{"10.0.0.0/8", "192.0.2.0/24"}) || !reflect.DeepEqual(props.GeoIP, []string{"FR", "US"}) {
		t.Fatalf("list labels not parsed: %#v", props)
	}
}

func TestParseDesiredTunnelsIgnoresUnrelatedAndMalformedLabels(t *testing.T) {
	info := ContainerInfo{
		ID:   "abc",
		Name: "web",
		Labels: map[string]string{
			"com.example.owner":        "platform",
			"rstream.tunnel.malformed": "ignored",
			"rstream.tunnel..forward":  "ignored",
		},
	}
	desired, err := ParseDesiredTunnels(info, "", runmodel.ResolvedContext{Engine: "engine", Token: "token"})
	if err != nil {
		t.Fatalf("ParseDesiredTunnels() error = %v", err)
	}
	if desired != nil {
		t.Fatalf("desired = %#v, want nil", desired)
	}
}

func TestParseDesiredTunnelsRejectsInvalidSecurityLabels(t *testing.T) {
	tests := []struct {
		name      string
		labels    map[string]string
		wantError string
	}{
		{
			name: "empty custom label key",
			labels: map[string]string{
				"rstream.tunnel.app.forward": "8080",
				"rstream.tunnel.app.label.":  "bad",
			},
			wantError: "label key is empty",
		},
		{
			name: "unknown http label",
			labels: map[string]string{
				"rstream.tunnel.app.forward":      "8080",
				"rstream.tunnel.app.http.unknown": "true",
			},
			wantError: "unknown http label",
		},
		{
			name: "unknown tls label",
			labels: map[string]string{
				"rstream.tunnel.app.forward":     "8080",
				"rstream.tunnel.app.tls.unknown": "true",
			},
			wantError: "unknown tls label",
		},
		{
			name: "conflicting upstream tls aliases",
			labels: map[string]string{
				"rstream.tunnel.app.forward":          "8080",
				"rstream.tunnel.app.upstream-tls":     "true",
				"rstream.tunnel.app.http.upstreamTLS": "false",
			},
			wantError: "conflicts",
		},
		{
			name: "missing mtls ca file",
			labels: map[string]string{
				"rstream.tunnel.app.forward":            "8080",
				"rstream.tunnel.app.tls.mtlsCACertFile": filepath.Join(t.TempDir(), "missing.pem"),
			},
			wantError: "mtlsCACertFile is not supported",
		},
		{
			name: "host loopback forward",
			labels: map[string]string{
				"rstream.tunnel.app.forward": "127.0.0.1:22",
			},
			wantError: "outside the discovered container network",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := ContainerInfo{ID: "abc", Name: "web", Labels: tt.labels, Networks: map[string]string{"default": "10.0.0.2"}}
			_, err := ParseDesiredTunnels(info, "default", runmodel.ResolvedContext{Engine: "engine", Token: "token"})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("ParseDesiredTunnels() error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestParseDesiredTunnelsHTTPAuthAndGate(t *testing.T) {
	info := ContainerInfo{
		ID:   "abc",
		Name: "web",
		Labels: map[string]string{
			"rstream.tunnel.app.forward":               "8080",
			"rstream.tunnel.app.protocol":              "http",
			"rstream.tunnel.app.http.auth.token":       "true",
			"rstream.tunnel.app.http.auth.rstream":     "false",
			"rstream.tunnel.app.http.gate.challenge":   "true",
			"rstream.tunnel.app.http.upstreamTLS":      "false",
			"rstream.tunnel.app.http.version":          "http/1.1",
			"rstream.tunnel.app.label.environment":     "dev",
			"rstream.tunnel.app.label.service-version": "v1",
		},
		Networks: map[string]string{"default": "10.0.0.2"},
	}
	desired, err := ParseDesiredTunnels(info, "default", runmodel.ResolvedContext{Engine: "engine", Token: "token"})
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
		t.Fatalf("expected token auth to be enabled")
	}
	if props.RstreamAuth == nil || *props.RstreamAuth != false {
		t.Fatalf("expected rstream auth to be disabled")
	}
	if props.ChallengeMode == nil || *props.ChallengeMode != true {
		t.Fatalf("expected challenge mode to be enabled")
	}
}

func TestParseDesiredTunnelsRejectsHTTPSettingsOnNonHTTPProtocol(t *testing.T) {
	info := ContainerInfo{
		ID:   "abc",
		Name: "web",
		Labels: map[string]string{
			"rstream.tunnel.app.forward":             "8080",
			"rstream.tunnel.app.protocol":            "tls",
			"rstream.tunnel.app.http.auth.token":     "true",
			"rstream.tunnel.app.http.gate.challenge": "true",
		},
		Networks: map[string]string{"default": "10.0.0.2"},
	}
	_, err := ParseDesiredTunnels(info, "default", runmodel.ResolvedContext{Engine: "engine", Token: "token"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "http labels require protocol") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDesiredTunnelsRejectsUnsupportedUpstreamTLSAliases(t *testing.T) {
	info := ContainerInfo{
		ID:   "abc",
		Name: "web",
		Labels: map[string]string{
			"rstream.tunnel.app.forward":     "8080",
			"rstream.tunnel.app.upstreamTLS": "true",
		},
		Networks: map[string]string{"default": "10.0.0.2"},
	}
	_, err := ParseDesiredTunnels(info, "default", runmodel.ResolvedContext{Engine: "engine", Token: "token"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), `unknown label "upstreamTLS"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDesiredTunnelsRejectsLegacyAuthLabels(t *testing.T) {
	info := ContainerInfo{
		ID:   "abc",
		Name: "web",
		Labels: map[string]string{
			"rstream.tunnel.app.forward":      "8080",
			"rstream.tunnel.app.auth.token":   "true",
			"rstream.tunnel.app.auth.rstream": "true",
		},
		Networks: map[string]string{"default": "10.0.0.2"},
	}
	_, err := ParseDesiredTunnels(info, "default", runmodel.ResolvedContext{Engine: "engine", Token: "token"})
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), `unknown label "auth.`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContainerInfoFirstIPIsStableAndSkipsBlankValues(t *testing.T) {
	info := ContainerInfo{Networks: map[string]string{
		"z-last": "10.0.0.9",
		"a":      " ",
		"b":      "10.0.0.2",
	}}
	if got := info.FirstIP(); got != "10.0.0.2" {
		t.Fatalf("got %q", got)
	}
	if got := (ContainerInfo{}).FirstIP(); got != "" {
		t.Fatalf("empty networks should return empty IP, got %q", got)
	}
}

func TestResolveContainerHostPrecedence(t *testing.T) {
	info := ContainerInfo{Name: "web", Networks: map[string]string{"backend": "10.0.0.2", "frontend": "10.0.0.3"}}
	if got := resolveContainerHost(info, "backend"); got != "10.0.0.2" {
		t.Fatalf("network-specific IP not preferred: %q", got)
	}
	if got := resolveContainerHost(info, "missing"); got != "10.0.0.2" {
		t.Fatalf("first IP fallback not used: %q", got)
	}
	if got := resolveContainerHost(info, ""); got != "10.0.0.2" {
		t.Fatalf("first IP should be used without network filter: %q", got)
	}
	if got := resolveContainerHost(ContainerInfo{}, ""); got != "" {
		t.Fatalf("empty info should not resolve a host: %q", got)
	}
}

func TestParserPrimitives(t *testing.T) {
	for _, input := range []string{" true ", "YES", "1", "t", "y"} {
		got, err := parseBool(input)
		if err != nil || !got {
			t.Fatalf("parseBool(%q)=%v,%v", input, got, err)
		}
	}
	for _, input := range []string{" false ", "NO", "0", "f", "n"} {
		got, err := parseBool(input)
		if err != nil || got {
			t.Fatalf("parseBool(%q)=%v,%v", input, got, err)
		}
	}
	if _, err := parseBool("maybe"); err == nil {
		t.Fatalf("expected invalid boolean error")
	}
	if got := splitCSV(" a, ,b ,, c "); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("unexpected splitCSV result: %#v", got)
	}
	if got := splitCSV(" , "); got != nil {
		t.Fatalf("blank CSV should return nil, got %#v", got)
	}
	if !isBarePort("8080") || isBarePort("127.0.0.1:8080") || isBarePort("") {
		t.Fatalf("unexpected bare port detection")
	}
}

func TestEnumParsersAcceptTrimmedCaseInsensitiveInput(t *testing.T) {
	protocol, err := parseProtocol(" HTTP ")
	if err != nil || protocol != rstream.ProtocolHTTP {
		t.Fatalf("parseProtocol got %q err=%v", protocol, err)
	}
	tunnelType, err := parseTunnelType(" DATAGRAM ")
	if err != nil || tunnelType != rstream.TunnelTypeDatagram {
		t.Fatalf("parseTunnelType got %q err=%v", tunnelType, err)
	}
	httpVersion, err := parseHTTPVersion(" H3 ")
	if err != nil || httpVersion != rstream.HTTP3 {
		t.Fatalf("parseHTTPVersion got %q err=%v", httpVersion, err)
	}
	tlsMode, err := parseTLSMode(" PASSTHROUGH ")
	if err != nil || tlsMode != rstream.TLSModePassthrough {
		t.Fatalf("parseTLSMode got %q err=%v", tlsMode, err)
	}
	tlsMinVersion, err := parseTLSMinVersion(" TLS1.3 ")
	if err != nil || tlsMinVersion != "tls1.3" {
		t.Fatalf("parseTLSMinVersion got %q err=%v", tlsMinVersion, err)
	}
	for name, fn := range map[string]func(string) error{
		"protocol": func(v string) error { _, err := parseProtocol(v); return err },
		"type":     func(v string) error { _, err := parseTunnelType(v); return err },
		"http":     func(v string) error { _, err := parseHTTPVersion(v); return err },
		"tlsMode":  func(v string) error { _, err := parseTLSMode(v); return err },
		"tlsMin":   func(v string) error { _, err := parseTLSMinVersion(v); return err },
	} {
		if err := fn("invalid"); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Fatalf("%s parser should reject invalid input, got %v", name, err)
		}
	}
}

func TestResolveForwardBarePortRequiresResolvableContainerHost(t *testing.T) {
	_, err := resolveForward("8080", ContainerInfo{}, "")
	if err == nil || !strings.Contains(err.Error(), "unable to resolve container host") {
		t.Fatalf("expected host resolution error, got %v", err)
	}
	target, err := resolveForward("8443", ContainerInfo{Name: "web", Networks: map[string]string{"default": "10.0.0.2"}}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Host != "10.0.0.2" || target.Port != "8443" {
		t.Fatalf("unexpected target: %#v", target)
	}
}

func TestResolveForwardDoesNotTrustContainerNameAsHost(t *testing.T) {
	info := ContainerInfo{
		Name:     "127.0.0.1",
		Networks: map[string]string{"default": "10.0.0.2"},
	}
	if _, err := resolveForward("127.0.0.1:22", info, "default"); err == nil || !strings.Contains(err.Error(), "outside the discovered container network") {
		t.Fatalf("expected explicit loopback host to be rejected, got %v", err)
	}
	target, err := resolveForward("8080", info, "default")
	if err != nil {
		t.Fatalf("bare port should resolve to Docker network IP: %v", err)
	}
	if target.Host != "10.0.0.2" {
		t.Fatalf("bare port resolved to %q, want Docker network IP", target.Host)
	}
}
