// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func TestDoctorEngineHostPort(t *testing.T) {
	host, address, err := doctorEngineHostPort("project.example.com:8443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host != "project.example.com" || address != "project.example.com:8443" {
		t.Fatalf("unexpected address: host=%q address=%q", host, address)
	}
	host, address, err = doctorEngineHostPort("https://project.example.com")
	if err != nil {
		t.Fatalf("unexpected URL error: %v", err)
	}
	if host != "project.example.com" || address != "project.example.com:443" {
		t.Fatalf("unexpected URL address: host=%q address=%q", host, address)
	}
}

func TestParseDoctorTokenInfo(t *testing.T) {
	expiresAt := time.Unix(2000000000, 0).UTC()
	token := doctorTestJWT(map[string]any{
		"exp":           expiresAt.Unix(),
		"permissions":   []string{"tunnels.resources.read-only", "account.projects.read-only"},
		"scope":         "openid profile",
		"tunnelsGrants": []map[string]any{{"projects": []string{"project-id"}}},
	})
	info, err := parseDoctorTokenInfo(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ExpiresAt == nil || !info.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("unexpected expiry: %#v", info.ExpiresAt)
	}
	if len(info.Permissions) != 2 || info.Permissions[0] != "account.projects.read-only" {
		t.Fatalf("unexpected permissions: %#v", info.Permissions)
	}
	if len(info.Scopes) != 2 || info.Scopes[0] != "openid" || info.Scopes[1] != "profile" {
		t.Fatalf("unexpected scopes: %#v", info.Scopes)
	}
	if !info.HasTunnelGrants {
		t.Fatal("expected tunnel grants")
	}
}

func TestDoctorUsesControlPlane(t *testing.T) {
	if !doctorUsesControlPlane(config.Resolved{}) {
		t.Fatal("expected no-context runtime to use control plane")
	}
	if !doctorUsesControlPlane(config.Resolved{Context: &config.Context{APIURL: "https://rstream.io"}}) {
		t.Fatal("expected API-linked context to use control plane")
	}
	if doctorUsesControlPlane(config.Resolved{Context: &config.Context{Engine: "project.example.com:443"}}) {
		t.Fatal("expected engine-only context to skip control plane")
	}
}

func TestDoctorQUICTransportDetectionCopiesSettings(t *testing.T) {
	localAddr := "127.0.0.1"
	forceIPv4 := true
	dnsOverride := "1.1.1.1:53"
	transport, ok := doctorQUICTransport(&rstream.QUICTransport{
		LocalAddr:   &localAddr,
		ForceIPv4:   &forceIPv4,
		DNSOverride: &dnsOverride,
	})
	if !ok {
		t.Fatal("expected QUIC transport")
	}
	if transport.LocalAddr == nil || *transport.LocalAddr != localAddr {
		t.Fatalf("unexpected local address: %#v", transport.LocalAddr)
	}
	if transport.ForceIPv4 == nil || !*transport.ForceIPv4 {
		t.Fatalf("unexpected ForceIPv4: %#v", transport.ForceIPv4)
	}
	if transport.DNSOverride == nil || *transport.DNSOverride != dnsOverride {
		t.Fatalf("unexpected DNS override: %#v", transport.DNSOverride)
	}
	if _, ok := doctorQUICTransport(nil); ok {
		t.Fatal("nil transport should not be detected as QUIC")
	}
}

func TestApplyEnvTransportOverridesEnablesQUIC(t *testing.T) {
	resolved := applyEnvTransportOverrides(config.Resolved{}, config.EnvSettings{UseQUIC: true})
	if _, ok := resolved.Transport.(*rstream.QUICTransport); !ok {
		t.Fatalf("expected QUIC transport, got %#v", resolved.Transport)
	}
}

func doctorTestJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
}
