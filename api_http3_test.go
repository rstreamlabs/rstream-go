// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func serveAPIHTTP3(t *testing.T, addr string, cfg *tls.Config, handler http.Handler) {
	t.Helper()
	packet, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	server := &http3.Server{TLSConfig: cfg.Clone(), Handler: handler}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(packet) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = packet.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("HTTP/3 server did not stop")
		}
	})
}

func TestMTLSAPIHTTP3DiscoveryInventoryAndSSE(t *testing.T) {
	for _, mode := range []string{"quic", "auto"} {
		t.Run(mode, func(t *testing.T) {
			f := newDiscoveryFixture(t, nil, true, false)
			if mode == "quic" {
				f.client.Transport = &QUICTransport{}
			} else {
				f.client.Transport = &AutoTransport{}
			}
			var ordinaryH3, dedicatedH3 atomic.Int32
			serveAPIHTTP3(t, f.ordinary.Listener.Addr().String(), f.ordinary.TLS, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 3 {
					t.Error("discovery did not use HTTP/3")
				}
				ordinaryH3.Add(1)
				f.ordinary.Config.Handler.ServeHTTP(w, r)
			}))
			serveAPIHTTP3(t, f.dedicated.Listener.Addr().String(), f.dedicated.TLS, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 3 {
					t.Error("API did not use HTTP/3")
				}
				dedicatedH3.Add(1)
				f.dedicated.Config.Handler.ServeHTTP(w, r)
			}))
			if _, err := f.client.ListClients(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			if _, err := f.client.ListTunnels(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			if err := f.client.WatchSSE(t.Context(), nil, func(Event) error { return io.EOF }); !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			if ordinaryH3.Load() != 1 || dedicatedH3.Load() != 3 || f.connections.Load() != 0 || f.certificates.Load() != 1 {
				t.Fatalf("discovery=%d API=%d TCP=%d certificate callbacks=%d", ordinaryH3.Load(), dedicatedH3.Load(), f.connections.Load(), f.certificates.Load())
			}
			api, discovery := f.client.apiHTTP3, f.client.discoveryHTTP3
			if err := f.client.Close(); err != nil {
				t.Fatal(err)
			}
			for _, pool := range []*apiHTTP3Transport{api, discovery} {
				pool.mu.Lock()
				remaining := len(pool.dialers)
				pool.mu.Unlock()
				if remaining != 0 {
					t.Fatal("Close leaked QUIC socket owners")
				}
			}
		})
	}
}

func TestAPIHTTP3AutoDoesNotFallbackAfterSendingMutation(t *testing.T) {
	f := newDiscoveryFixture(t, nil, true, false)
	f.client.MTLSAPIURL = f.dedicated.URL + "/api"
	var sent atomic.Int32
	serveAPIHTTP3(t, f.dedicated.Listener.Addr().String(), f.dedicated.TLS, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		sent.Add(1)
		panic(http.ErrAbortHandler)
	}))
	_, _, err := f.client.apiDo(t.Context(), http.MethodPost, "/mutation", nil, strings.NewReader(`{"ttlSeconds":600}`), nil, nil)
	if err == nil || sent.Load() != 1 || f.client.apiHTTP3.useTCP.Load() {
		t.Fatalf("ambiguous mutation was replayed or lost: sent=%d err=%v", sent.Load(), err)
	}
}

func TestAPIHTTP3AutoDoesNotFallbackOnUntrustedServer(t *testing.T) {
	f := newDiscoveryFixture(t, nil, true, false)
	f.client.TLSClientConfig.RootCAs = x509.NewCertPool()
	serveAPIHTTP3(t, f.ordinary.Listener.Addr().String(), f.ordinary.TLS, f.ordinary.Config.Handler)
	if _, err := f.client.EngineAPIURL(t.Context()); err == nil {
		t.Fatal("untrusted HTTP/3 discovery accepted")
	}
	if f.connections.Load() != 0 || f.discoveries.Load() != 0 || f.certificates.Load() != 0 {
		t.Fatal("TLS validation failure retried over TCP or disclosed identity")
	}
}

func TestAPIHTTP3AutoFallbackPreservesMutationBody(t *testing.T) {
	f := newDiscoveryFixture(t, nil, true, false)
	f.client.MTLSAPIURL = f.dedicated.URL + "/api"
	var calls atomic.Int32
	f.dedicated.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"ttlSeconds":600}` {
			t.Errorf("body=%q err=%v", body, err)
		}
		calls.Add(1)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	_, status, err := f.client.apiDo(t.Context(), http.MethodPost, "/mutation", nil, strings.NewReader(`{"ttlSeconds":600}`), nil, nil)
	if err != nil || status != 200 || calls.Load() != 1 || !f.client.apiHTTP3.useTCP.Load() {
		t.Fatalf("fallback failed: calls=%d status=%d err=%v", calls.Load(), status, err)
	}
}

func TestAPIHTTP3CloseCancelsPendingHandshake(t *testing.T) {
	f := newDiscoveryFixture(t, nil, true, false)
	f.client.Transport = &QUICTransport{}
	packet, err := net.ListenPacket("udp", f.ordinary.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	started := make(chan struct{})
	go func() { buffer := make([]byte, 2048); _, _, _ = packet.ReadFrom(buffer); close(started) }()
	done := make(chan error, 1)
	go func() { _, err := f.client.EngineAPIURL(context.Background()); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("QUIC handshake did not start")
	}
	if err := f.client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Close left QUIC handshake running")
	}
}
