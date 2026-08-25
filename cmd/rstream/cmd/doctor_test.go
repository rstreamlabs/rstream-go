// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	if err := doctorReportError(report); err == nil {
		t.Fatal("doctorReportError() should reject a report with failed checks")
	}
	healthyReport := doctorReport{}
	healthyReport.add("config", doctorStatusPass, "ok", nil)
	healthyReport.finalize()
	if err := doctorReportError(healthyReport); err != nil {
		t.Fatalf("doctorReportError() = %v", err)
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
	auto := &rstream.AutoTransport{TLS: &rstream.Transport{ForceIPv4: rstream.BoolPtr(true)}, QUIC: &rstream.QUICTransport{LocalAddr: rstream.StringPtr("127.0.0.1")}}
	if transport, ok := doctorQUICTransport(auto); !ok || transport.LocalAddr == nil || *transport.LocalAddr != "127.0.0.1" {
		t.Fatalf("doctorQUICTransport(auto) = %#v, %v", transport, ok)
	}
	if transport, ok := doctorTLSTransport(auto); !ok || transport.ForceIPv4 == nil || !*transport.ForceIPv4 {
		t.Fatalf("doctorTLSTransport(auto) = %#v, %v", transport, ok)
	}
}

func TestDoctorTransportProbeStatuses(t *testing.T) {
	tlsStatus, quicStatus := doctorTransportProbeStatuses(rstream.TunnelTransportModeAuto, true, false)
	if tlsStatus != doctorStatusPass || quicStatus != doctorStatusWarn {
		t.Fatalf("auto TLS fallback statuses = %s, %s", tlsStatus, quicStatus)
	}
	tlsStatus, quicStatus = doctorTransportProbeStatuses(rstream.TunnelTransportModeQUIC, true, false)
	if tlsStatus != doctorStatusPass || quicStatus != doctorStatusFail {
		t.Fatalf("strict QUIC statuses = %s, %s", tlsStatus, quicStatus)
	}
	tlsStatus, quicStatus = doctorTransportProbeStatuses(rstream.TunnelTransportModeTLS, false, true)
	if tlsStatus != doctorStatusFail || quicStatus != doctorStatusPass {
		t.Fatalf("strict TLS statuses = %s, %s", tlsStatus, quicStatus)
	}
}

func TestRunDoctorWithoutContextStaysLocalAndReportsActionableChecks(t *testing.T) {
	clearRstreamTestEnv(t)
	path := filepath.Join(t.TempDir(), "missing.yaml")
	command := runtimeFlagsCommand(t)
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
	if got["config"] != doctorStatusPass || got["context"] != doctorStatusWarn || got["token"] != doctorStatusFail || got["engine_address"] != doctorStatusSkip || got["engine"] != doctorStatusSkip {
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

func TestDoctorEngineAggregatesHealthAndInventory(t *testing.T) {
	var inventoryRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/health/live":
			_, _ = w.Write([]byte(`{"status":"live"}`))
		case "/api/health/ready":
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		case "/api/clients":
			inventoryRequests.Add(1)
			_, _ = w.Write([]byte(`[{"id":"client-1"}]`))
		case "/api/tunnels":
			inventoryRequests.Add(1)
			_, _ = w.Write([]byte(`[{"id":"tunnel-1","status":"online"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report := doctorReport{}
	checkDoctorEngine(t.Context(), &report, doctorTestResolved(server))
	if len(report.Checks) != 1 || report.Checks[0].Name != "engine" || report.Checks[0].Status != doctorStatusPass || report.Checks[0].Message != "engine is ready" {
		t.Fatalf("engine check = %#v", report.Checks)
	}
	if report.Checks[0].Details["clients"] != "1" || report.Checks[0].Details["tunnels"] != "1" || report.Checks[0].Details["onlineTunnels"] != "1" {
		t.Fatalf("engine details = %#v", report.Checks[0].Details)
	}
	if inventoryRequests.Load() != 2 {
		t.Fatalf("inventory requests = %d, want 2", inventoryRequests.Load())
	}
}

func TestDoctorEngineStopsWhenRuntimeIsUnavailable(t *testing.T) {
	var inventoryRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health/live":
			_, _ = w.Write([]byte(`{"status":"live"}`))
		case "/api/health/ready":
			http.Error(w, `{"status":"unavailable"}`, http.StatusServiceUnavailable)
		default:
			inventoryRequests.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	report := doctorReport{}
	checkDoctorEngine(t.Context(), &report, doctorTestResolved(server))
	if len(report.Checks) != 1 || report.Checks[0].Name != "engine" || report.Checks[0].Status != doctorStatusFail || report.Checks[0].Message != "engine is running but unavailable" {
		t.Fatalf("engine check = %#v", report.Checks)
	}
	if inventoryRequests.Load() != 0 {
		t.Fatalf("inventory requests = %d, want 0", inventoryRequests.Load())
	}
}

func TestProbeDoctorTunnelCreationExercisesLifecycle(t *testing.T) {
	tunnel := &doctorTestTunnel{props: rstream.TunnelProperties{ID: rstream.StringPtr("tunnel-1")}}
	control := &doctorTestControlChannel{tunnel: tunnel}
	details, err := probeDoctorTunnelCreation(t.Context(), &doctorTestTunnelClient{control: control})
	if err != nil {
		t.Fatalf("probeDoctorTunnelCreation() error = %v", err)
	}
	if details["tunnelId"] != "tunnel-1" || details["mode"] != "private" || tunnel.closeCalls != 1 || control.closeCalls != 1 {
		t.Fatalf("unexpected lifecycle: details=%#v tunnelClose=%d controlClose=%d", details, tunnel.closeCalls, control.closeCalls)
	}
	if control.props.Type == nil || *control.props.Type != rstream.TunnelTypeBytestream || control.props.Publish == nil || *control.props.Publish {
		t.Fatalf("unexpected tunnel properties: %#v", control.props)
	}
}

func TestProbeDoctorTunnelCreationFallsBackWhenPrivateIsUnavailable(t *testing.T) {
	privateControl := &doctorTestControlChannel{createErr: &rstream.EngineError{Code: rstream.EngineErrorCodeFeatureNotAvailable, Message: "private tunnels are unavailable"}}
	tunnel := &doctorTestTunnel{props: rstream.TunnelProperties{ID: rstream.StringPtr("tunnel-2")}}
	publishedControl := &doctorTestControlChannel{tunnel: tunnel}
	client := &doctorTestTunnelClient{controls: []rstream.ControlChannel{privateControl, publishedControl}}
	details, err := probeDoctorTunnelCreation(t.Context(), client)
	if err != nil {
		t.Fatalf("probeDoctorTunnelCreation() error = %v", err)
	}
	if details["tunnelId"] != "tunnel-2" || details["mode"] != "published_http" || details["fallbackReason"] != "private_feature_unavailable" {
		t.Fatalf("unexpected fallback details: %#v", details)
	}
	if client.connectCalls != 2 || privateControl.closeCalls != 1 || publishedControl.closeCalls != 1 || tunnel.closeCalls != 1 {
		t.Fatalf("unexpected lifecycle: connects=%d privateClose=%d publishedClose=%d tunnelClose=%d", client.connectCalls, privateControl.closeCalls, publishedControl.closeCalls, tunnel.closeCalls)
	}
	if publishedControl.props.Type == nil || *publishedControl.props.Type != rstream.TunnelTypeBytestream || publishedControl.props.Publish == nil || !*publishedControl.props.Publish || publishedControl.props.Protocol == nil || *publishedControl.props.Protocol != rstream.ProtocolHTTP || publishedControl.props.HTTPVersion == nil || *publishedControl.props.HTTPVersion != rstream.HTTP1_1 {
		t.Fatalf("unexpected fallback tunnel properties: %#v", publishedControl.props)
	}
}

func TestProbeDoctorTunnelCreationClosesControlAfterFailure(t *testing.T) {
	control := &doctorTestControlChannel{createErr: errors.New("create failed")}
	_, err := probeDoctorTunnelCreation(t.Context(), &doctorTestTunnelClient{control: control})
	if err == nil || !strings.Contains(err.Error(), "creating private tunnel") {
		t.Fatalf("probeDoctorTunnelCreation() error = %v", err)
	}
	if control.closeCalls != 1 {
		t.Fatalf("control close calls = %d, want 1", control.closeCalls)
	}
}

func doctorTestResolved(server *httptest.Server) config.Resolved {
	return config.Resolved{
		Engine: strings.TrimPrefix(server.URL, "https://"),
		Token:  "token",
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
}

func doctorToken(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

type doctorTestTunnelClient struct {
	control      rstream.ControlChannel
	controls     []rstream.ControlChannel
	err          error
	connectCalls int
}

func (c *doctorTestTunnelClient) Connect(context.Context, *rstream.Config) (rstream.ControlChannel, error) {
	c.connectCalls++
	if len(c.controls) > 0 {
		index := c.connectCalls - 1
		if index >= len(c.controls) {
			return nil, errors.New("unexpected control channel connection")
		}
		return c.controls[index], nil
	}
	return c.control, c.err
}

type doctorTestControlChannel struct {
	tunnel     rstream.Tunnel
	createErr  error
	closeErr   error
	closeCalls int
	props      rstream.TunnelProperties
}

func (c *doctorTestControlChannel) CreateTunnel(_ context.Context, props rstream.TunnelProperties) (rstream.Tunnel, error) {
	c.props = props
	return c.tunnel, c.createErr
}

func (c *doctorTestControlChannel) Close() error {
	c.closeCalls++
	return c.closeErr
}

func (c *doctorTestControlChannel) Done() <-chan error {
	return make(chan error)
}

func (c *doctorTestControlChannel) Err() error {
	return nil
}

func (c *doctorTestControlChannel) ServerDetails() *rstream.ServerDetails {
	return nil
}

type doctorTestTunnel struct {
	props      rstream.TunnelProperties
	closeErr   error
	closeCalls int
}

func (t *doctorTestTunnel) ForwardingAddress() (string, error) {
	return "", nil
}

func (t *doctorTestTunnel) Properties() (rstream.TunnelProperties, error) {
	return t.props, nil
}

func (t *doctorTestTunnel) Close() error {
	t.closeCalls++
	return t.closeErr
}
