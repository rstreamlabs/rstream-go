// See LICENSE file in the project root for license information.

package rundocker

import (
	"os"
	"path/filepath"
	"testing"

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
			name: "host port",
			info: ContainerInfo{
				ID:   "abc",
				Name: "web",
				Labels: map[string]string{
					"rstream.tunnel.app.forward": "127.0.0.1:9090",
				},
			},
			wantHost: "127.0.0.1",
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

func TestTLSCACertFileParsing(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(certPath, []byte("PEM-DATA"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	info := ContainerInfo{
		ID:   "abc",
		Name: "web",
		Labels: map[string]string{
			"rstream.tunnel.app.forward":            "8080",
			"rstream.tunnel.app.tls.mtls":           "true",
			"rstream.tunnel.app.tls.mtlsCACertFile": certPath,
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
	if desired[0].Props.MTLSCACertPEM == nil || *desired[0].Props.MTLSCACertPEM != "PEM-DATA" {
		t.Fatalf("expected PEM data to be loaded")
	}
}
