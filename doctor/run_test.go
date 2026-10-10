// See LICENSE file in the project root for license information.

package doctor_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/doctor"
)

func checksByName(report doctor.Report) map[string]doctor.Check {
	checks := make(map[string]doctor.Check)
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	return checks
}

func TestRunUsesOnlyProvidedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("invalid: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RSTREAM_CONFIG", path)
	t.Setenv("RSTREAM_AUTHENTICATION_TOKEN", "ambient-secret")
	t.Setenv("RSTREAM_ENGINE", "ambient.invalid:443")
	for _, deep := range []bool{false, true} {
		report, err := doctor.Run(t.Context(), config.Resolved{}, doctor.Options{Deep: deep})
		if !errors.Is(err, doctor.ErrChecksFailed) || !errors.Is(report.Err(), doctor.ErrChecksFailed) {
			t.Fatalf("missing credentials error: %v", err)
		}
		checks := checksByName(report)
		if report.ConfigPath != "" || report.Engine != "" || checks["token"].Status != doctor.StatusFail || checks["engine"].Status != doctor.StatusSkip {
			t.Fatalf("used ambient configuration: %+v", report)
		}
		_, gotDeep := checks["tunnel_creation"]
		if gotDeep != deep || deep && checks["tunnel_creation"].Status != doctor.StatusSkip {
			t.Fatalf("deep option: %+v", checks)
		}
		data, err := json.Marshal(report)
		if err != nil || strings.Contains(string(data), "ambient") {
			t.Fatalf("invalid report: %s %v", data, err)
		}
		var decoded doctor.Report
		if err := json.Unmarshal(data, &decoded); err != nil || decoded.Summary != report.Summary || decoded.GeneratedAt.IsZero() {
			t.Fatalf("JSON round trip: %v", err)
		}
	}
}

func TestRunHealthyEngineAndConcurrentResourceOwnership(t *testing.T) {
	var active, peak, requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-secret-credential" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/health/live":
			_, _ = w.Write([]byte(`{"status":"live"}`))
		case "/api/health/ready":
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		case "/api/clients", "/api/tunnels":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	server.TLS = &tls.Config{NextProtos: []string{"rstrm/1", "http/1.1"}}
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			n := active.Add(1)
			for previous := peak.Load(); n > previous; previous = peak.Load() {
				if peak.CompareAndSwap(previous, n) {
					break
				}
			}
		} else if state == http.StateClosed {
			active.Add(-1)
		}
	}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	resolved := config.Resolved{Engine: strings.TrimPrefix(server.URL, "https://"), Token: "fixture-secret-credential", ContextName: "local", Context: &config.Context{Name: "local"}, Transport: &rstream.Transport{}, TLSClientConfig: &tls.Config{RootCAs: roots}}
	const workers = 4
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for range 2 {
				report, err := doctor.Run(t.Context(), resolved, doctor.Options{})
				if err != nil || report.Summary.Fail != 0 || report.Summary.Warn != 1 {
					t.Errorf("healthy engine: %+v, %v", report, err)
					return
				}
				checks := checksByName(report)
				if checks["tls"].Status != doctor.StatusPass || checks["engine"].Status != doctor.StatusPass || checks["tunnel_transport"].Details["selectedMode"] != "tls" {
					t.Errorf("transport checks: %+v", checks)
				}
				data, err := json.Marshal(report)
				if err != nil || strings.Contains(string(data), resolved.Token) {
					t.Errorf("credential in report or invalid JSON: %v", err)
				}
			}
		})
	}
	group.Wait()
	deadline := time.After(3 * time.Second)
	for active.Load() != 0 {
		select {
		case <-deadline:
			t.Fatalf("diagnostics leaked %d connections", active.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if peak.Load() > workers*2 || requests.Load() != workers*2*4 {
		t.Fatalf("unbounded connections or unexpected requests: peak=%d requests=%d", peak.Load(), requests.Load())
	}
	if resolved.TLSClientConfig.ServerName != "" || len(resolved.TLSClientConfig.NextProtos) != 0 || resolved.Context.Name != "local" {
		t.Fatal("diagnostics mutated the caller's configuration")
	}
}

func TestRunCancellationPreservesPartialReport(t *testing.T) {
	for _, alreadyCanceled := range []bool{true, false} {
		t.Run(map[bool]string{true: "before", false: "during"}[alreadyCanceled], func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				close(started)
				<-r.Context().Done()
				close(stopped)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if alreadyCanceled {
				cancel()
			} else {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			bounded, stop := context.WithTimeout(ctx, 3*time.Second)
			defer stop()
			report, err := doctor.Run(bounded, config.Resolved{APIURL: server.URL, Token: "fixture-token"}, doctor.Options{Deep: true})
			if !errors.Is(err, context.Canceled) || !errors.Is(err, doctor.ErrChecksFailed) || checksByName(report)["execution"].Status != doctor.StatusFail {
				t.Fatalf("cancellation: %+v %v", report, err)
			}
			if alreadyCanceled {
				if requests.Load() != 0 || len(report.Checks) != 1 {
					t.Fatal("canceled run performed diagnostics")
				}
			} else {
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("canceled request was not closed")
				}
				if requests.Load() != 1 || len(report.Checks) < 3 {
					t.Fatal("lost partial results or ran probes after cancellation")
				}
			}
		})
	}
}

func TestRunRegionalProjectFailureSkipsDeepNetworkProbes(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Deployment-Access") != "fixture-header" || r.Header.Get("Authorization") != "Bearer fixture-token" {
			http.Error(w, "access headers missing", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/whoami" {
			_, _ = w.Write([]byte(`{"id":"user-1","role":"owner"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"project-1","name":"project","endpoint":"project","status":"error","routing":"global","domain":"global.invalid","enginePort":443,"regionalEndpoints":[{"region":"eu-west-3","domain":"eu.invalid","enginePort":8443}],"issue":{"code":"maintenance","message":"Under maintenance"}}`))
	}))
	defer server.Close()
	resolved := config.Resolved{APIURL: server.URL, Token: "fixture-token", Region: "eu-west-3", Engine: "original.invalid:443", Context: &config.Context{Name: "project", APIURL: server.URL, ProjectEndpoint: "project"}, ControlPlaneHeaders: map[string]string{"X-Deployment-Access": "fixture-header"}}
	report, err := doctor.Run(t.Context(), resolved, doctor.Options{Deep: true})
	checks := checksByName(report)
	if !errors.Is(err, doctor.ErrChecksFailed) || report.Engine != "project.eu.invalid:8443" || checks["project"].Details["issueCode"] != "maintenance" {
		t.Fatalf("regional project diagnostics: %+v %v", report, err)
	}
	for _, name := range []string{"dns", "tls", "quic_transport", "engine", "tunnel_creation"} {
		if checks[name].Status != doctor.StatusSkip {
			t.Fatalf("%s was probed on an inactive project", name)
		}
	}
	if requests.Load() != 3 || resolved.Engine != "original.invalid:443" {
		t.Fatalf("unexpected requests or mutated input: %d %s", requests.Load(), resolved.Engine)
	}
}

type failingTransport struct {
	dials  atomic.Int32
	closes atomic.Int32
}

func (d *failingTransport) Dial(context.Context, string, *tls.Config) (net.Conn, error) {
	d.dials.Add(1)
	return nil, errors.New("fixture transport failure")
}

func (d *failingTransport) Close() error {
	d.closes.Add(1)
	return nil
}

func TestRunDeepUsesButDoesNotCloseCallerTransport(t *testing.T) {
	transport := &failingTransport{}
	resolved := config.Resolved{Engine: "127.0.0.1:1", Token: "fixture-token", Context: &config.Context{Name: "local"}, Transport: transport}
	for _, deep := range []bool{false, true} {
		before := transport.dials.Load()
		report, err := doctor.Run(t.Context(), resolved, doctor.Options{Deep: deep})
		checks := checksByName(report)
		if !errors.Is(err, doctor.ErrChecksFailed) || checks["engine"].Status != doctor.StatusFail {
			t.Fatalf("unreachable engine: %+v %v", report, err)
		}
		check, exists := checks["tunnel_creation"]
		if exists != deep || deep && (check.Status != doctor.StatusFail || !strings.Contains(check.Details["error"], "fixture transport failure")) {
			t.Fatalf("deep execution: %+v", checks)
		}
		if deep && transport.dials.Load() <= before || transport.closes.Load() != 0 {
			t.Fatalf("caller transport: deep=%v before=%d dials=%d closes=%d checks=%+v", deep, before, transport.dials.Load(), transport.closes.Load(), checks)
		}
	}
}

func TestRunPreservesDeadlineError(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	report, err := doctor.Run(ctx, config.Resolved{}, doctor.Options{})
	if !errors.Is(err, context.DeadlineExceeded) || report.Summary.Fail != 1 {
		t.Fatalf("deadline: %+v %v", report, err)
	}
}
