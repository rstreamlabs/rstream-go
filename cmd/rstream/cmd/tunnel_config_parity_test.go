// See LICENSE file in the project root for license information.

package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runapply"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/rundocker"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestForwardOptionsCoverRunYAML(t *testing.T) {
	mappings := map[string]string{
		"publish": "publish", "no-publish": "publish", "bytestream": "type", "datagram": "type",
		"tls": "protocol", "tcp": "protocol", "dtls": "protocol", "quic": "protocol", "http": "protocol",
		"tcp-port": "port", "allow-cross-region-routing": "allowCrossRegionRouting", "label": "labels",
		"geoip": "geoip", "trusted-ips": "trustedIPs", "host": "host", "tls-mode": "tls.mode",
		"tls-alpn": "tls.alpns", "tls-min-version": "tls.minVersion", "tls-ciphers": "tls.ciphers", "mtls": "tls.mtls",
		"http-version": "http.version", "upstream-tls": "upstreamTLS",
		"datagram-guaranteed-delivery": "datagramGuaranteedDelivery", "token-auth": "http.auth.token", "rstream-auth": "http.auth.rstream", "challenge-mode": "http.gate.challenge",
	}
	exclusions := map[string]string{
		"name": "run entry name", "output": "interactive rendering only", "retry": "run reconciles desired state", "no-retry": "run reconciles desired state", "retry-interval": "run reconciliation interval is command-wide",
	}
	paths := yamlLeafPaths(reflect.TypeOf(runapply.TunnelSpec{}), "")
	covered := map[string]bool{}
	cmd := &cobra.Command{Use: "forward"}
	addForwardFlags(cmd)
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if path, ok := mappings[flag.Name]; ok {
			if !paths[path] {
				t.Errorf("unknown YAML mapping %s -> %s", flag.Name, path)
			}
			covered[path] = true
			delete(mappings, flag.Name)
		} else if exclusions[flag.Name] == "" {
			t.Errorf("flag without YAML mapping or intentional exclusion: %s", flag.Name)
		}
	})
	for path := range paths {
		if !covered[path] {
			t.Errorf("YAML option without CLI mapping: %s", path)
		}
	}
	if len(mappings) > 0 {
		t.Errorf("mappings without flags: %v", mappings)
	}
}

func TestTunnelConfigurationEquivalentSources(t *testing.T) {
	cases := []struct {
		name    string
		flags   []string
		yaml    string
		docker  map[string]string
		want    rstream.TunnelProperties
		wantErr string
	}{
		{
			name:  "HTTP and TLS complete surface",
			flags: []string{"--http", "--publish", "--bytestream", "--host=app.example.com", "--http-version=h2c", "--upstream-tls=false", "--token-auth", "--rstream-auth=false", "--challenge-mode", "--mtls", "--tls-mode=terminated", "--tls-min-version=tls1.3", "--tls-alpn=h2,http/1.1", "--tls-ciphers=TLS_AES_128_GCM_SHA256", "--label=tier=edge", "--geoip=FR,US", "--trusted-ips=192.0.2.0/24", "--allow-cross-region-routing=false"},
			yaml: `publish: true
protocol: http
type: bytestream
host: app.example.com
upstreamTLS: false
allowCrossRegionRouting: false
labels: {tier: edge}
geoip: [FR, US]
trustedIPs: [192.0.2.0/24]
http: {version: h2c, auth: {token: true, rstream: false}, gate: {challenge: true}}
tls: {mode: terminated, minVersion: tls1.3, alpns: [h2, http/1.1], ciphers: [TLS_AES_128_GCM_SHA256], mtls: true}`,
			docker: map[string]string{"publish": "true", "protocol": "http", "type": "bytestream", "host": "app.example.com", "upstream-tls": "false", "allow-cross-region-routing": "false", "label.tier": "edge", "geoip": "FR,US", "trusted-ips": "192.0.2.0/24", "http.version": "h2c", "http.auth.token": "true", "http.auth.rstream": "false", "http.gate.challenge": "true", "tls.mode": "terminated", "tls.minVersion": "tls1.3", "tls.alpns": "h2,http/1.1", "tls.ciphers": "TLS_AES_128_GCM_SHA256", "tls.mtls": "true"},
			want:   rstream.TunnelProperties{Publish: rstream.BoolPtr(true), Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP), Type: rstream.TunnelTypePtr(rstream.TunnelTypeBytestream), Hostname: rstream.StringPtr("app.example.com"), UpstreamTLS: rstream.BoolPtr(false), AllowCrossRegionRouting: rstream.BoolPtr(false), Labels: map[string]string{"tier": "edge"}, GeoIP: []string{"FR", "US"}, TrustedIPs: []string{"192.0.2.0/24"}, HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP2), TokenAuth: rstream.BoolPtr(true), RstreamAuth: rstream.BoolPtr(false), ChallengeMode: rstream.BoolPtr(true), TLSMode: rstream.TLSModePtr(rstream.TLSModeTerminated), TLSMinVersion: rstream.StringPtr("tls1.3"), TLSALPNs: []string{"h2", "http/1.1"}, TLSCiphers: []string{"TLS_AES_128_GCM_SHA256"}, MTLSAuth: rstream.BoolPtr(true)},
		},
		{name: "explicit private HTTP", flags: []string{"--http", "--publish=false"}, yaml: "protocol: http\npublish: false", docker: map[string]string{"protocol": "http", "publish": "false"}, want: rstream.TunnelProperties{Protocol: rstream.ProtocolPtr(rstream.ProtocolHTTP), Publish: rstream.BoolPtr(false)}},
		{name: "TCP reserved port", flags: []string{"--tcp", "--tcp-port=8080"}, yaml: "protocol: tcp\nport: 8080", docker: map[string]string{"protocol": "tcp", "port": "8080"}, want: rstream.TunnelProperties{Protocol: rstream.ProtocolPtr(rstream.ProtocolTCP), Type: rstream.TunnelTypePtr(rstream.TunnelTypeBytestream), Publish: rstream.BoolPtr(true), Port: rstream.Uint32Ptr(8080)}},
		{name: "reliable datagram", flags: []string{"--quic", "--datagram", "--publish", "--datagram-guaranteed-delivery=false"}, yaml: "protocol: quic\ntype: datagram\npublish: true\ndatagramGuaranteedDelivery: false", docker: map[string]string{"protocol": "quic", "type": "datagram", "publish": "true", "datagram-guaranteed-delivery": "false"}, want: rstream.TunnelProperties{Protocol: rstream.ProtocolPtr(rstream.ProtocolQUIC), Type: rstream.TunnelTypePtr(rstream.TunnelTypeDatagram), Publish: rstream.BoolPtr(true), DatagramGuaranteedDelivery: rstream.BoolPtr(false)}},
		{name: "non HTTP auth", flags: []string{"--tls", "--token-auth"}, yaml: "protocol: tls\nhttp: {auth: {token: true}}", docker: map[string]string{"protocol": "tls", "http.auth.token": "true"}, wantErr: "http settings require protocol"},
		{name: "private TCP", flags: []string{"--tcp", "--publish=false"}, yaml: "protocol: tcp\npublish: false", docker: map[string]string{"protocol": "tcp", "publish": "false"}, wantErr: "requires a published tunnel"},
		{name: "bytestream delivery", flags: []string{"--http", "--bytestream", "--datagram-guaranteed-delivery"}, yaml: "protocol: http\ntype: bytestream\ndatagramGuaranteedDelivery: true", docker: map[string]string{"protocol": "http", "type": "bytestream", "datagram-guaranteed-delivery": "true"}, wantErr: "requires a datagram tunnel"},
		{name: "empty host", flags: []string{"--http", "--host="}, yaml: "protocol: http\nhost: ''", docker: map[string]string{"protocol": "http", "host": ""}, wantErr: "host must not be empty"},
		{name: "empty TLS mode", flags: []string{"--http", "--tls-mode="}, yaml: "protocol: http\ntls: {mode: ''}", docker: map[string]string{"protocol": "http", "tls.mode": ""}, wantErr: "invalid tls mode"},
		{name: "empty HTTP version", flags: []string{"--http", "--http-version="}, yaml: "protocol: http\nhttp: {version: ''}", docker: map[string]string{"protocol": "http", "http.version": ""}, wantErr: "invalid http version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, source := range []string{"cli", "yaml", "docker"} {
				t.Run(source, func(t *testing.T) {
					var got rstream.TunnelProperties
					var err error
					switch source {
					case "cli":
						cmd := &cobra.Command{Use: "forward"}
						addForwardFlags(cmd)
						if err = cmd.ParseFlags(tc.flags); err != nil {
							t.Fatal(err)
						}
						var props *rstream.TunnelProperties
						props, err = newTunnelPropertiesFromFlags(cmd)
						if props != nil {
							got = *props
						}
					case "yaml":
						path := filepath.Join(t.TempDir(), "run.yaml")
						data := "version: 1\ntunnels:\n  - name: app\n    forward: 8080\n    tunnel:\n      " + strings.ReplaceAll(tc.yaml, "\n", "\n      ") + "\n"
						if err = os.WriteFile(path, []byte(data), 0600); err != nil {
							t.Fatal(err)
						}
						var desired []runmodel.DesiredTunnel
						desired, err = runapply.DesiredTunnels(path, runmodel.ResolvedContext{Engine: "engine.example.com:443", Token: "test"}, nil)
						if len(desired) > 0 {
							got = desired[0].Props
						}
					case "docker":
						labels := map[string]string{"rstream.tunnel.app.forward": "8080"}
						for key, value := range tc.docker {
							labels["rstream.tunnel.app."+key] = value
						}
						var desired []runmodel.DesiredTunnel
						desired, err = rundocker.ParseDesiredTunnels(rundocker.ContainerInfo{Name: "test", Labels: labels, Networks: map[string]string{"default": "192.0.2.1"}}, "", runmodel.ResolvedContext{})
						if len(desired) > 0 {
							got = desired[0].Props
						}
					}
					if tc.wantErr != "" {
						if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
							t.Fatalf("want %q, got %v", tc.wantErr, err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					got.Name = nil
					delete(got.Labels, runmodel.ManagedByLabel)
					delete(got.Labels, runmodel.SourceLabel)
					if len(got.Labels) == 0 {
						got.Labels = nil
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("effective options differ:\ngot: %#v\nwant: %#v", got, tc.want)
					}
				})
			}
		})
	}
}

func TestTunnelConfigurationRejectsUnknownUpstreamTLSOptions(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		cmd := &cobra.Command{Use: "forward"}
		addForwardFlags(cmd)
		if err := cmd.ParseFlags([]string{"--http", "--http-use-tls=" + value}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("CLI accepted removed option: %v", err)
		}
		path := filepath.Join(t.TempDir(), "run.yaml")
		data := "version: 1\ntunnels:\n  - name: app\n    forward: 8080\n    tunnel:\n      protocol: http\n      http: {upstreamTLS: " + value + "}\n"
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := runapply.LoadConfig(path); err == nil || !strings.Contains(err.Error(), "field upstreamTLS not found") {
			t.Fatalf("YAML accepted removed option: %v", err)
		}
		_, err := rundocker.ParseDesiredTunnels(rundocker.ContainerInfo{Name: "test", Labels: map[string]string{"rstream.tunnel.app.forward": "8080", "rstream.tunnel.app.http.upstreamTLS": value}, Networks: map[string]string{"default": "192.0.2.1"}}, "", runmodel.ResolvedContext{})
		if err == nil || !strings.Contains(err.Error(), "unknown http label") {
			t.Fatalf("Docker accepted removed option: %v", err)
		}
	}
}
