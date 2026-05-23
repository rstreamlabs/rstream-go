// See LICENSE file in the project root for license information.

package cmd

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func TestDoctorReportSummaryAndAddressHelpers(t *testing.T) {
	report := doctorReport{}
	report.add("config", doctorStatusPass, "ok", nil)
	report.add("token", doctorStatusWarn, "warn", nil)
	report.add("engine", doctorStatusFail, "fail", nil)
	report.add("project", doctorStatusSkip, "skip", nil)
	report.finalize()
	if report.Summary.Pass != 1 || report.Summary.Warn != 1 || report.Summary.Fail != 1 || report.Summary.Skip != 1 {
		t.Fatalf("unexpected summary: %#v", report.Summary)
	}
	host, address, err := doctorEngineHostPort("https://engine.example.com")
	if err != nil || host != "engine.example.com" || address != "engine.example.com:443" {
		t.Fatalf("doctorEngineHostPort(url) = %q %q %v", host, address, err)
	}
	host, address, err = doctorEngineHostPort("engine.example.com:8443")
	if err != nil || host != "engine.example.com" || address != "engine.example.com:8443" {
		t.Fatalf("doctorEngineHostPort(hostport) = %q %q %v", host, address, err)
	}
	if _, _, err := doctorEngineHostPort("bad:addr:443"); err == nil {
		t.Fatalf("expected invalid address error")
	}
}

func TestDoctorTokenParsingAndChecks(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Unix()
	token := doctorToken(map[string]any{
		"exp":         expiresAt,
		"permissions": []string{"write", "read"},
		"scope":       "openid profile",
		"resources":   map[string]any{"tunnels": map[string]any{"projects": []string{"project-1"}}},
	})
	info, err := parseDoctorTokenInfo(token)
	if err != nil {
		t.Fatalf("parseDoctorTokenInfo() error = %v", err)
	}
	if info.ExpiresAt == nil || !info.HasResources || strings.Join(info.Permissions, ",") != "read,write" || strings.Join(info.Scopes, ",") != "openid,profile" {
		t.Fatalf("unexpected token info: %#v", info)
	}
	report := doctorReport{}
	checkDoctorToken(&report, token)
	if len(report.Checks) != 1 || report.Checks[0].Status != doctorStatusPass {
		t.Fatalf("token check should pass: %#v", report.Checks)
	}
	report = doctorReport{}
	checkDoctorToken(&report, doctorToken(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}))
	if len(report.Checks) != 1 || report.Checks[0].Status != doctorStatusFail {
		t.Fatalf("expired token should fail: %#v", report.Checks)
	}
	report = doctorReport{}
	checkDoctorToken(&report, "")
	if len(report.Checks) != 1 || report.Checks[0].Status != doctorStatusFail {
		t.Fatalf("missing token should fail: %#v", report.Checks)
	}
}

func TestDoctorContextAndTransportHelpers(t *testing.T) {
	report := doctorReport{}
	checkDoctorContext(&report, config.Resolved{APIURL: "https://api.example.com"})
	if len(report.Checks) != 1 || report.Checks[0].Status != doctorStatusWarn {
		t.Fatalf("missing context should warn: %#v", report.Checks)
	}
	report = doctorReport{}
	resolved := config.Resolved{APIURL: "https://api.example.com", Engine: "engine.example.com:443", Context: &config.Context{Name: "demo", APIURL: "https://api.example.com", ProjectEndpoint: "demo.rstream.io"}}
	checkDoctorContext(&report, resolved)
	if len(report.Checks) != 1 || report.Checks[0].Status != doctorStatusPass {
		t.Fatalf("context should pass: %#v", report.Checks)
	}
	if !doctorUsesControlPlane(resolved) {
		t.Fatalf("linked context should use Control plane API")
	}
	resolved.Context.APIURL = ""
	if doctorUsesControlPlane(resolved) {
		t.Fatalf("unlinked context should not use Control plane API")
	}
	transport, ok := doctorQUICTransport(&rstream.QUICTransport{LocalAddr: rstream.StringPtr("127.0.0.1")})
	if !ok || transport.LocalAddr == nil || *transport.LocalAddr != "127.0.0.1" {
		t.Fatalf("doctorQUICTransport() = %#v, %v", transport, ok)
	}
	if _, ok := doctorQUICTransport(nil); ok {
		t.Fatalf("nil transport should not be QUIC")
	}
}

func TestRunDoctorWithoutContextStaysLocalAndReportsActionableChecks(t *testing.T) {
	clearRstreamTestEnv(t)
	path := filepath.Join(t.TempDir(), "missing.yaml")
	command := runtimeFlagsCommand()
	mustSetFlag(t, command, "config", path)
	report := runDoctor(command)
	if report.ConfigPath != path {
		t.Fatalf("ConfigPath = %q, want %q", report.ConfigPath, path)
	}
	if report.Summary.Pass == 0 || report.Summary.Fail == 0 || report.Summary.Skip == 0 {
		t.Fatalf("summary should include pass/fail/skip checks: %#v", report.Summary)
	}
	got := map[string]doctorStatus{}
	for _, check := range report.Checks {
		got[check.Name] = check.Status
	}
	if got["config"] != doctorStatusPass || got["context"] != doctorStatusWarn || got["token"] != doctorStatusFail || got["engine_address"] != doctorStatusSkip || got["engine_inventory"] != doctorStatusSkip {
		t.Fatalf("unexpected doctor checks: %#v", got)
	}
	file, err := os.CreateTemp(t.TempDir(), "doctor-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer file.Close()
	if err := printDoctorTable(file, report); err != nil {
		t.Fatalf("printDoctorTable() error = %v", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatalf("Seek() error = %v", err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "Summary") || !strings.Contains(string(data), "config") {
		t.Fatalf("doctor table output = %q", string(data))
	}
}

func TestDoctorTLSAndTunnelHelpers(t *testing.T) {
	if tlsVersionName(tls.VersionTLS13) != "TLS 1.3" || tlsVersionName(0x1234) != "0x1234" {
		t.Fatalf("unexpected TLS version names")
	}
	tunnels := []rstream.TunnelInventory{{Status: "online"}, {Status: "offline"}, {Status: "online"}}
	if countDoctorOnlineTunnels(tunnels) != 2 {
		t.Fatalf("online tunnel count mismatch")
	}
	if values := doctorStringSliceClaim([]any{" read ", "", 42, "write"}); strings.Join(values, ",") != "read,write" {
		t.Fatalf("doctorStringSliceClaim() = %#v", values)
	}
	if n, ok := doctorNumberClaim(json.Number("42")); !ok || n != 42 {
		t.Fatalf("doctorNumberClaim(json.Number) = %f, %v", n, ok)
	}
	if _, ok := doctorNumberClaim("42"); ok {
		t.Fatalf("doctorNumberClaim(string) should be rejected")
	}
}

func doctorToken(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
