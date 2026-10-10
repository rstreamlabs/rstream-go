// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type discoveryFixture struct {
	client       *Client
	ordinary     *httptest.Server
	dedicated    *httptest.Server
	discoveries  atomic.Int32
	certificates atomic.Int32
	connections  atomic.Int32
}

func newDiscoveryFixture(t *testing.T, discovery http.HandlerFunc, callback bool, http2 bool) *discoveryFixture {
	t.Helper()
	f := &discoveryFixture{}
	upgrader := websocket.Upgrader{}
	f.dedicated = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) != 1 || r.Header.Get("Authorization") != "" || r.URL.Query().Has("rstream.token") {
			t.Error("dedicated API did not receive certificate-only authentication")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/clients", "/api/tunnels":
			_, _ = io.WriteString(w, "[]")
		case "/api/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"type\":\"state.initial\",\"object\":{}}\n\n")
		case "/api/websocket":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"state.initial","object":{}}`)); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	f.dedicated.EnableHTTP2 = http2
	f.dedicated.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	f.dedicated.StartTLS()
	t.Cleanup(f.dedicated.Close)
	f.ordinary = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.discoveries.Add(1)
		if r.URL.Path != engineDiscoveryPath || len(r.TLS.PeerCertificates) != 0 || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Control-Plane") != "" {
			t.Error("discovery leaked identity or used an unexpected path")
		}
		if discovery != nil {
			discovery(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "projectEndpoint": "127", "mtlsApiUrl": f.dedicated.URL + "/api", "capabilities": []string{"engine-api-mtls"}})
	}))
	f.ordinary.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			f.connections.Add(1)
		}
	}
	f.ordinary.EnableHTTP2 = http2
	f.ordinary.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	f.ordinary.StartTLS()
	t.Cleanup(f.ordinary.Close)
	roots := x509.NewCertPool()
	roots.AddCert(f.ordinary.Certificate())
	roots.AddCert(f.dedicated.Certificate())
	certificate := f.dedicated.TLS.Certificates[0]
	cfg := &tls.Config{RootCAs: roots}
	if callback {
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			f.certificates.Add(1)
			return &certificate, nil
		}
	} else {
		cfg.Certificates = []tls.Certificate{certificate}
	}
	var err error
	f.client, err = NewClient(ClientOptions{Engine: strings.TrimPrefix(f.ordinary.URL, "https://"), TLSClientConfig: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.client.Close() })
	return f
}

func TestMTLSAPIInventoryAndWatchShareDiscovery(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, callback := range []bool{false, true} {
			name := "http1"
			if http2 {
				name = "http2"
			}
			if callback {
				name += "/callback"
			} else {
				name += "/static"
			}
			t.Run(name, func(t *testing.T) {
				f := newDiscoveryFixture(t, nil, callback, http2)
				if _, err := f.client.ListClients(t.Context(), nil); err != nil {
					t.Fatal(err)
				}
				if _, err := f.client.ListTunnels(t.Context(), nil); err != nil {
					t.Fatal(err)
				}
				for _, transport := range []string{"sse", "websocket"} {
					err := f.client.Watch(t.Context(), transport, nil, func(event Event) error {
						if event.Type != "state.initial" {
							t.Errorf("event = %#v", event)
						}
						return io.EOF
					})
					if !errors.Is(err, io.EOF) {
						t.Fatalf("%s: %v", transport, err)
					}
				}
				if got := f.discoveries.Load(); got != 1 {
					t.Fatalf("discovery calls = %d", got)
				}
				if callback && f.certificates.Load() < 1 {
					t.Fatal("certificate callback was not used for the dedicated API")
				}
			})
		}
	}
}

func TestMTLSDiscoveryCoalescesConcurrentCallsAndCaches(t *testing.T) {
	f := newDiscoveryFixture(t, nil, true, true)
	var group sync.WaitGroup
	for range 64 {
		group.Go(func() {
			base, err := f.client.EngineAPIURL(t.Context())
			if err != nil || base != f.dedicated.URL+"/api" {
				t.Errorf("discovery = %q, %v", base, err)
			}
		})
	}
	group.Wait()
	if f.discoveries.Load() != 1 || f.connections.Load() != 1 || f.certificates.Load() != 0 {
		t.Fatalf("requests=%d, connections=%d, client certificate callbacks=%d", f.discoveries.Load(), f.connections.Load(), f.certificates.Load())
	}
	f.client.invalidateEngineDiscovery()
	if _, err := f.client.EngineAPIURL(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.discoveries.Load() != 2 || f.connections.Load() != 1 {
		t.Fatal("refresh did not reuse discovery pool")
	}
	if err := f.client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.EngineAPIURL(t.Context()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed discovery: %v", err)
	}
}

func TestMTLSDiscoveryCancellationAndClose(t *testing.T) {
	entered := make(chan struct{})
	f := newDiscoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}, true, true)
	leader := make(chan error, 1)
	go func() { _, err := f.client.EngineAPIURL(t.Context()); leader <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("discovery did not start")
	}
	ctx, cancel := context.WithCancel(t.Context())
	waiter := make(chan error, 1)
	go func() { _, err := f.client.EngineAPIURL(ctx); waiter <- err }()
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not canceled")
	}
	if err := f.client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-leader:
		if err == nil {
			t.Fatal("closed discovery succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Close left discovery running")
	}
	if f.connections.Load() != 1 {
		t.Fatal("waiter opened another connection")
	}
}

func TestMTLSDiscoveryRejectsInvalidDocumentsAndRedirects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{"missing", "", 404},
		{"redirect", "", 302},
		{"version", `{"version":2,"projectEndpoint":"127","mtlsApiUrl":"https://127.example/api","capabilities":["engine-api-mtls"]}`, 200},
		{"project", `{"version":1,"projectEndpoint":"other","mtlsApiUrl":"https://127.example/api","capabilities":["engine-api-mtls"]}`, 200},
		{"target-project", `{"version":1,"projectEndpoint":"127","mtlsApiUrl":"https://other.example/api","capabilities":["engine-api-mtls"]}`, 200},
		{"http", `{"version":1,"projectEndpoint":"127","mtlsApiUrl":"http://127.example/api","capabilities":["engine-api-mtls"]}`, 200},
		{"capability", `{"version":1,"projectEndpoint":"127","mtlsApiUrl":"https://127.example/api"}`, 200},
		{"oversized", strings.Repeat(" ", 16385), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
			defer target.Close()
			f := newDiscoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}, true, false)
			for range 2 {
				if _, err := f.client.EngineAPIURL(t.Context()); err == nil {
					t.Fatal("invalid discovery succeeded")
				}
			}
			if f.discoveries.Load() != 2 || f.certificates.Load() != 0 || redirected.Load() != 0 {
				t.Fatal("failed discovery cached, leaked identity or followed redirect")
			}
		})
	}
}

func TestMTLSDiscoveryVerifiesTLSAndOverrideSkipsDiscovery(t *testing.T) {
	f := newDiscoveryFixture(t, nil, true, false)
	f.client.TLSClientConfig.RootCAs = nil
	f.client.TLSClientConfig.InsecureSkipVerify = true
	if _, err := f.client.EngineAPIURL(t.Context()); err == nil {
		t.Fatal("untrusted discovery accepted")
	}
	if f.discoveries.Load() != 0 || f.certificates.Load() != 0 {
		t.Fatal("untrusted authority received identity or HTTP request")
	}
	f.client.MTLSAPIURL = f.dedicated.URL + "/api"
	if _, err := f.client.ListClients(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if f.discoveries.Load() != 0 {
		t.Fatal("explicit URL invoked discovery")
	}
}

func TestDiscoveryCacheLifetimeAndURLValidation(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   time.Duration
	}{
		{"", time.Hour}, {"public, max-age=120", 2 * time.Minute}, {"max-age=999999999", 24 * time.Hour}, {"no-store, max-age=3600", 0}, {"max-age=3600, no-cache", 0}, {"max-age=-1", 0}, {"max-age=oops", 0},
	} {
		if got := discoveryCacheLifetime(tc.header); got != tc.want {
			t.Errorf("%q = %v, want %v", tc.header, got, tc.want)
		}
	}
	for _, raw := range []string{"http://project.test/api", "https://user:pass@project.test/api", "https://project.test/api?secret=x", "https://project.test/api#fragment", "https://project.test:0/api", "https://project.test:65536/api", "https://project.test/other", "https://project.test/%61pi"} {
		if _, err := parseEngineAPIURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
