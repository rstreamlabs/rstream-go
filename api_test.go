// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCloneProxyHTTPHeadersCopiesMap(t *testing.T) {
	headers := map[string]string{"X-Trace": "a"}
	clone := cloneProxyHTTPHeaders(headers)
	headers["X-Trace"] = "changed"
	clone["X-New"] = "b"
	if clone["X-Trace"] != "a" {
		t.Fatalf("clone should preserve original value, got %#v", clone)
	}
	if _, ok := headers["X-New"]; ok {
		t.Fatalf("clone mutation leaked into source map")
	}
	if cloneProxyHTTPHeaders(nil) != nil {
		t.Fatalf("nil headers should stay nil")
	}
}

func TestAPIDialerClonesTransportState(t *testing.T) {
	tlsCfg := &tls.Config{ServerName: "proxy.example.com", NextProtos: []string{"h2"}}
	headers := map[string]string{"X-Trace": "source"}
	forceIPv4 := true
	client := &Client{Transport: &Transport{ProxyHTTPHeaders: headers, TLSProxyConfig: tlsCfg, ForceIPv4: &forceIPv4}}
	dialer, ok := client.apiDialer().(*Transport)
	if !ok {
		t.Fatalf("apiDialer() returned %T, want *Transport", client.apiDialer())
	}
	headers["X-Trace"] = "mutated"
	tlsCfg.ServerName = "mutated.example.com"
	forceIPv4 = false
	if dialer.ProxyHTTPHeaders["X-Trace"] != "source" {
		t.Fatalf("proxy headers should be cloned, got %#v", dialer.ProxyHTTPHeaders)
	}
	if dialer.TLSProxyConfig == tlsCfg || dialer.TLSProxyConfig.ServerName != "proxy.example.com" {
		t.Fatalf("TLS proxy config should be cloned, got %#v", dialer.TLSProxyConfig)
	}
	if dialer.ForceIPv4 == nil || !*dialer.ForceIPv4 || dialer.ForceIPv4 == client.Transport.(*Transport).ForceIPv4 {
		t.Fatalf("ForceIPv4 was not cloned")
	}
}

func TestAPIDialerMapsQUICTransportToTCPTransport(t *testing.T) {
	headers := map[string]string{"X-Trace": "source"}
	tlsCfg := &tls.Config{ServerName: "proxy.example.com"}
	client := &Client{Transport: &QUICTransport{
		LocalAddr:        StringPtr("127.0.0.1"),
		ForceIPv6:        BoolPtr(true),
		DNSOverride:      StringPtr("1.1.1.1:853"),
		DNSOverTLS:       BoolPtr(true),
		DNSServerName:    StringPtr("cloudflare-dns.com"),
		DNSSECEnabled:    BoolPtr(true),
		ProxyHTTP:        StringPtr("https://masque.example.com:443"),
		ProxySOCKS5:      StringPtr("socks5://socks.example.com:1080"),
		ProxyUsername:    StringPtr("user"),
		ProxyPassword:    StringPtr("pass"),
		ProxyHTTPHeaders: headers,
		TLSProxyConfig:   tlsCfg,
	}}
	dialer, ok := client.apiDialer().(*Transport)
	if !ok {
		t.Fatalf("apiDialer() returned %T, want *Transport", client.apiDialer())
	}
	if dialer.LocalAddr == nil || *dialer.LocalAddr != "127.0.0.1" || dialer.ForceIPv6 == nil || !*dialer.ForceIPv6 {
		t.Fatalf("local/IP family settings not preserved: %#v", dialer)
	}
	if dialer.DNSOverride == nil || *dialer.DNSOverride != "1.1.1.1:853" || dialer.DNSOverTLS == nil || !*dialer.DNSOverTLS {
		t.Fatalf("DNS settings not preserved: %#v", dialer)
	}
	if dialer.DNSServerName == nil || *dialer.DNSServerName != "cloudflare-dns.com" || dialer.DNSSECEnabled == nil || !*dialer.DNSSECEnabled {
		t.Fatalf("advanced DNS settings not preserved: %#v", dialer)
	}
	headers["X-Trace"] = "mutated"
	tlsCfg.ServerName = "mutated.example.com"
	*client.Transport.(*QUICTransport).LocalAddr = "mutated"
	*client.Transport.(*QUICTransport).ForceIPv6 = false
	if dialer.ProxyHTTP == nil || *dialer.ProxyHTTP != "https://masque.example.com:443" || dialer.ProxySOCKS5 == nil || *dialer.ProxySOCKS5 != "socks5://socks.example.com:1080" {
		t.Fatalf("proxy settings not preserved: %#v", dialer)
	}
	if dialer.ProxyHTTPHeaders["X-Trace"] != "source" {
		t.Fatalf("proxy headers should be cloned, got %#v", dialer.ProxyHTTPHeaders)
	}
	if *dialer.LocalAddr != "127.0.0.1" || !*dialer.ForceIPv6 || dialer.LocalAddr == client.Transport.(*QUICTransport).LocalAddr || dialer.ForceIPv6 == client.Transport.(*QUICTransport).ForceIPv6 {
		t.Fatalf("QUIC scalar pointers should be cloned: %#v", dialer)
	}
	if dialer.TLSProxyConfig == tlsCfg || dialer.TLSProxyConfig.ServerName != "proxy.example.com" {
		t.Fatalf("TLS proxy config should be cloned, got %#v", dialer.TLSProxyConfig)
	}
}

func TestAPIDialerMapsAutoTransportToTCPWithoutSelectingTunnelTransport(t *testing.T) {
	auto := &AutoTransport{TLS: &Transport{ForceIPv4: BoolPtr(true), ProxyHTTPHeaders: map[string]string{"X-Test": "value"}}, QUIC: &QUICTransport{}}
	client := &Client{Transport: auto}
	dialer, ok := client.apiDialer().(*Transport)
	if !ok || dialer.ForceIPv4 == nil || !*dialer.ForceIPv4 || dialer.ProxyHTTPHeaders["X-Test"] != "value" {
		t.Fatalf("apiDialer() = %#v, want cloned TLS child", dialer)
	}
	if auto.SelectedTransport() != nil {
		t.Fatal("Engine API dialer must not select the tunnel transport")
	}
}

func TestSetQueryJSON(t *testing.T) {
	q := url.Values{}
	if err := setQueryJSON(q, "params", nil); err != nil {
		t.Fatalf("nil query value should not error: %v", err)
	}
	if len(q) != 0 {
		t.Fatalf("nil query value should not modify query: %#v", q)
	}
	if err := setQueryJSON(q, "params", map[string]string{"name": "demo"}); err != nil {
		t.Fatalf("setQueryJSON() error = %v", err)
	}
	if got := q.Get("params"); got != `{"name":"demo"}` {
		t.Fatalf("query value = %q", got)
	}
	if err := setQueryJSON(q, "bad", func() {}); err == nil || !strings.Contains(err.Error(), "encode bad") {
		t.Fatalf("expected JSON encoding error, got %v", err)
	}
}

func TestSSEConnReadAggregatesDataLines(t *testing.T) {
	body := strings.NewReader("event: ignored\n: comment\ndata: {\"type\":\"first\"}\ndata: {\"object\":{}}\n\n")
	conn := &sseConn{
		resp: &http.Response{Body: io.NopCloser(body)},
		rd:   bufio.NewReader(body),
	}
	got, err := conn.Read(t.Context())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(got) != "{\"type\":\"first\"}\n{\"object\":{}}" {
		t.Fatalf("Read() = %q", got)
	}
}

func TestSSEConnReadFlushesTrailingDataOnEOF(t *testing.T) {
	body := strings.NewReader("data: {\"type\":\"last\"}")
	conn := &sseConn{
		resp: &http.Response{Body: io.NopCloser(body)},
		rd:   bufio.NewReader(body),
	}
	got, err := conn.Read(t.Context())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(got) != `{"type":"last"}` {
		t.Fatalf("Read() = %q", got)
	}
}

func TestAPIClientRoundTripAgainstLocalTLSServer(t *testing.T) {
	token := "api-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.EscapedPath() {
		case "/api/auth":
			w.WriteHeader(http.StatusNoContent)
		case "/api/clients":
			if got := r.URL.Query().Get("params"); !strings.Contains(got, `"limit":2`) {
				t.Fatalf("client params query = %q", got)
			}
			_, _ = w.Write([]byte(`[{"id":"client-1","status":"online"}]`))
		case "/api/tunnels":
			if got := r.URL.Query().Get("params"); !strings.Contains(got, `"publish":true`) {
				t.Fatalf("tunnel params query = %q", got)
			}
			_, _ = w.Write([]byte(`[{"id":"tunnel-1","status":"online","client_id":"client-1"}]`))
		case "/api/tunnels/tunnel%2Fwith%20space":
			_, _ = w.Write([]byte(`{"id":"tunnel/with space","name":"demo"}`))
		case "/api/raw":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q", got)
			}
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testAPIClient(server, token)
	if engine, err := client.Login(t.Context()); err != nil || engine == nil || *engine != testAPIEngine(server) {
		t.Fatalf("Login() = %v, %v", engine, err)
	}
	if engine, err := client.Logout(t.Context()); err != nil || engine == nil || *engine != testAPIEngine(server) {
		t.Fatalf("Logout() = %v, %v", engine, err)
	}
	limit := 2
	clients, err := client.ListClients(t.Context(), &ListClientsParams{Limit: &limit})
	if err != nil {
		t.Fatalf("ListClients() error = %v", err)
	}
	if len(*clients) != 1 || (*clients)[0].ID != "client-1" {
		t.Fatalf("ListClients() = %#v", clients)
	}
	publish := true
	tunnels, err := client.ListTunnels(t.Context(), &ListTunnelsParams{Filters: &ListTunnelsFilters{Publish: &publish}})
	if err != nil {
		t.Fatalf("ListTunnels() error = %v", err)
	}
	if len(*tunnels) != 1 || (*tunnels)[0].ID == nil || *(*tunnels)[0].ID != "tunnel-1" {
		t.Fatalf("ListTunnels() = %#v", tunnels)
	}
	tunnel, err := client.GetTunnel(t.Context(), "tunnel/with space")
	if err != nil {
		t.Fatalf("GetTunnel() error = %v", err)
	}
	if tunnel.ID == nil || *tunnel.ID != "tunnel/with space" || tunnel.Name == nil || *tunnel.Name != "demo" {
		t.Fatalf("GetTunnel() = %#v", tunnel)
	}
	body := strings.NewReader(`{"hello":"world"}`)
	if data, status, err := client.apiDo(t.Context(), http.MethodPost, "/raw", nil, body, nil, nil); err != nil || status != http.StatusOK || string(data) != `{"ok":true}` {
		t.Fatalf("apiDo(raw) = %q, %d, %v", data, status, err)
	}
}

func TestAPIClientReusesConnections(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	client := testAPIClient(server, "token")
	for range 3 {
		if _, err := client.ListClients(t.Context(), nil); err != nil {
			t.Fatalf("ListClients() error = %v", err)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("API connections = %d, want one reused connection", got)
	}
}

func TestAPIClientRetriesOneTransientReadOnlyFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Length", "8")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	if _, err := client.ListTunnels(t.Context(), nil); err != nil {
		t.Fatalf("ListTunnels() error = %v", err)
	}
	if got := attempts.Load(); got != apiRequestAttempts {
		t.Fatalf("API attempts = %d, want %d", got, apiRequestAttempts)
	}
}

func TestAPIClientRetryRetainsBudgetAfterStalledAttempt(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	started := time.Now()
	if _, err := client.ListTunnels(t.Context(), nil); err != nil {
		t.Fatalf("ListTunnels() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= apiRequestTimeout {
		t.Fatalf("ListTunnels() took %s, want less than total timeout %s", elapsed, apiRequestTimeout)
	}
	if got := attempts.Load(); got != apiRequestAttempts {
		t.Fatalf("API attempts = %d, want %d", got, apiRequestAttempts)
	}
}

func TestAPIClientBoundsTransientReadOnlyRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Length", "8")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	if _, err := client.ListTunnels(t.Context(), nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ListTunnels() error = %v, want unexpected EOF", err)
	}
	if got := attempts.Load(); got != apiRequestAttempts {
		t.Fatalf("API attempts = %d, want %d", got, apiRequestAttempts)
	}
}

func TestAPIClientDoesNotRetryMutationsOrHTTPFailures(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Length", "8")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	if _, _, err := client.apiDo(t.Context(), http.MethodPost, "/raw", nil, strings.NewReader(`{}`), nil, nil); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("apiDo(POST) error = %v, want unexpected EOF", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("POST attempts = %d, want 1", got)
	}
	if _, err := client.ListTunnels(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("ListTunnels(503) error = %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts after 503 = %d, want 2", got)
	}
}

func TestAPIClientMutationRetainsTotalTimeout(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		time.Sleep(apiAttemptTimeout + 50*time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	if _, _, err := client.apiDo(t.Context(), http.MethodPost, "/raw", nil, strings.NewReader(`{}`), nil, nil); err != nil {
		t.Fatalf("apiDo(POST) error = %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("POST attempts = %d, want 1", got)
	}
}

func TestAPIClientCancellationPreventsTransientRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var attempts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		cancel()
		w.Header().Set("Content-Length", "8")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	if _, err := client.ListTunnels(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListTunnels() error = %v, want context canceled", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("API attempts = %d, want 1", got)
	}
}

func TestRetryableAPITransportErrorRejectsPermanentFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if retryableAPITransportError(canceled, 0, io.ErrUnexpectedEOF) {
		t.Fatal("canceled request must not retry")
	}
	if retryableAPITransportError(context.Background(), 0, &net.DNSError{Err: "not found", Name: "missing.example", IsNotFound: true}) {
		t.Fatal("permanent DNS failure must not retry")
	}
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	if retryableAPITransportError(expired, 0, context.DeadlineExceeded) {
		t.Fatal("expired total request context must not retry")
	}
	certificate := &x509.Certificate{}
	if retryableAPITransportError(context.Background(), 0, x509.HostnameError{Certificate: certificate, Host: "wrong.example"}) {
		t.Fatal("certificate hostname failure must not retry")
	}
	unknownAuthority := x509.UnknownAuthorityError{Cert: certificate}
	if retryableAPITransportError(context.Background(), 0, unknownAuthority) {
		t.Fatal("unknown certificate authority must not retry")
	}
	if retryableAPITransportError(context.Background(), 0, &tls.CertificateVerificationError{UnverifiedCertificates: []*x509.Certificate{certificate}, Err: unknownAuthority}) {
		t.Fatal("TLS certificate verification failure must not retry")
	}
	if retryableAPITransportError(context.Background(), http.StatusServiceUnavailable, io.ErrUnexpectedEOF) {
		t.Fatal("HTTP failures must not retry")
	}
	if !retryableAPITransportError(context.Background(), 0, context.DeadlineExceeded) {
		t.Fatal("bounded attempt timeout should retry while the total request context remains active")
	}
	if !retryableAPITransportError(context.Background(), http.StatusOK, io.ErrUnexpectedEOF) {
		t.Fatal("unexpected EOF should retry")
	}
}

func TestAPIClientReusesConnectionUnderParallelLoad(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	client := testAPIClient(server, "token")
	if _, err := client.ListClients(t.Context(), nil); err != nil {
		t.Fatalf("warm ListClients() error = %v", err)
	}
	const workers = 32
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.ListClients(t.Context(), nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ListClients() error = %v", err)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("API connections = %d, want one shared HTTP/2 connection", got)
	}
}

func TestAPIHTTPClientConcurrentInitialization(t *testing.T) {
	client := &Client{}
	const workers = 32
	transports := make(chan http.RoundTripper, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			httpClient, err := client.apiHttpClient()
			if err != nil {
				errs <- err
				return
			}
			transports <- httpClient.Transport
		}()
	}
	wg.Wait()
	close(errs)
	close(transports)
	for err := range errs {
		t.Fatalf("apiHttpClient() error = %v", err)
	}
	var transport http.RoundTripper
	for got := range transports {
		if transport == nil {
			transport = got
			continue
		}
		if got != transport {
			t.Fatalf("apiHttpClient() returned distinct transports %p and %p", transport, got)
		}
	}
}

func TestClientCloseIdleConnectionsRotatesOwnedAPITransport(t *testing.T) {
	client := &Client{}
	first, err := client.apiHttpClient()
	if err != nil {
		t.Fatalf("apiHttpClient() error = %v", err)
	}
	client.CloseIdleConnections()
	second, err := client.apiHttpClient()
	if err != nil {
		t.Fatalf("apiHttpClient() error = %v", err)
	}
	if first.Transport == second.Transport {
		t.Fatalf("API transport was retained after CloseIdleConnections()")
	}
}

func TestClientCloseIdleConnectionsIsConcurrentAndRepeatSafe(t *testing.T) {
	client := &Client{}
	const workers = 32
	var wg sync.WaitGroup
	for range workers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 100 {
				if _, err := client.apiHttpClient(); err != nil {
					t.Errorf("apiHttpClient() error = %v", err)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				client.CloseIdleConnections()
			}
		}()
	}
	wg.Wait()
	client.CloseIdleConnections()
}

func TestAPIClientErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/clients":
			_, _ = w.Write([]byte(`not-json`))
		case "/api/tunnels/missing":
			http.Error(w, "missing tunnel", http.StatusNotFound)
		default:
			http.Error(w, "", http.StatusTeapot)
		}
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	if _, err := client.ListClients(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("expected decode response error, got %v", err)
	}
	if _, err := client.GetTunnel(t.Context(), "missing"); err == nil || !strings.Contains(err.Error(), "missing tunnel") {
		t.Fatalf("expected API error body, got %v", err)
	}
	if _, err := client.GetTunnel(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "ID is required") {
		t.Fatalf("expected required ID error, got %v", err)
	}
	if _, _, err := (&Client{}).apiDo(t.Context(), http.MethodGet, "/auth", nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "engine URL is required") {
		t.Fatalf("expected missing engine error, got %v", err)
	}
	var missingContext context.Context
	if _, _, err := (&Client{}).apiDo(missingContext, http.MethodGet, "/auth", nil, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "context is required") {
		t.Fatalf("expected missing context error, got %v", err)
	}
}

func TestWatchSSEAgainstLocalTLSServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sse" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("rstream.token"); got != "" {
			t.Fatalf("rstream token query must not be set, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"state.initial\",\"object\":{\"ok\":true}}\n\n"))
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	var events []Event
	if err := client.WatchSSE(t.Context(), &WatchParams{}, func(ev Event) error {
		events = append(events, ev)
		return io.EOF
	}); err != io.EOF {
		t.Fatalf("WatchSSE() error = %v, want handler EOF", err)
	}
	if len(events) != 1 || events[0].Type != "state.initial" {
		t.Fatalf("events = %#v", events)
	}
}

func TestWatchWSAgainstLocalTLSServer(t *testing.T) {
	token := "api-token"
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("rstream.token"); got != "" {
			t.Fatalf("websocket token query must not be set, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("websocket authorization header = %q", got)
		}
		if got := r.URL.Query().Get("params"); !strings.Contains(got, `"status":"online"`) {
			t.Fatalf("websocket params = %q", got)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("websocket upgrade: %v", err)
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.PingMessage, []byte("ignored")); err != nil {
			t.Fatalf("write ping: %v", err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"client","object":{"id":"client-1"}}`)); err != nil {
			t.Fatalf("write event: %v", err)
		}
	}))
	defer server.Close()
	client := testAPIClient(server, token)
	errStop := errors.New("stop after first websocket event")
	var got Event
	err := client.WatchWS(t.Context(), &WatchParams{Clients: &ListClientsFilters{Status: StringPtr("online")}}, func(ev Event) error {
		got = ev
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("WatchWS() error = %v, want %v", err, errStop)
	}
	if got.Type != "client" || string(got.Object) != `{"id":"client-1"}` {
		t.Fatalf("websocket event = %#v", got)
	}
}

func TestWSConnConcurrentCloseJoinsPingLoop(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer close(serverDone)
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	watch := newWSConn(conn, time.Millisecond, time.Second)
	const callers = 64
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- watch.Close() }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	select {
	case <-watch.pingDone:
	default:
		t.Fatal("Close() returned before ping loop stopped")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not observe websocket closure")
	}
}

func TestOpenEventConnValidatesTransportAndTokenBeforeDial(t *testing.T) {
	engine := "engine.example.com:443"
	token := "api-token"
	client := &Client{EngineURL: &engine, Token: &token}
	if _, err := client.openEventConn(t.Context(), "tcp", nil); err == nil || !strings.Contains(err.Error(), "invalid transport") {
		t.Fatalf("openEventConn(invalid transport) = %v, want invalid transport error", err)
	}
	empty := ""
	client.Token = &empty
	if _, err := client.openEventConn(t.Context(), "sse", nil); err == nil || !strings.Contains(err.Error(), "missing authentication token") {
		t.Fatalf("openEventConn(empty token) = %v, want missing token error", err)
	}
	if _, err := (&Client{}).openEventConn(t.Context(), "sse", nil); err == nil || !strings.Contains(err.Error(), "engine URL is required") {
		t.Fatalf("openEventConn(missing engine) = %v, want engine error", err)
	}
}

func TestWatchSSEReturnsStatusAndInvalidJSONErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("rstream.token"); got != "" {
			t.Fatalf("rstream token query must not be set, got %q", got)
		}
		switch r.Header.Get("Authorization") {
		case "Bearer status":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "Bearer invalid-json":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testAPIClient(server, "status")
	if err := client.WatchSSE(t.Context(), nil, func(Event) error { return nil }); err == nil || !strings.Contains(err.Error(), "sse error (503): unavailable") {
		t.Fatalf("WatchSSE(status) = %v, want status error", err)
	}
	client = testAPIClient(server, "invalid-json")
	if err := client.WatchSSE(t.Context(), nil, func(Event) error { return nil }); err == nil || !strings.Contains(err.Error(), "invalid event json") {
		t.Fatalf("WatchSSE(invalid json) = %v, want JSON error", err)
	}
}

func TestWatchSSEClosesConnectionOnContextCancel(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	timer := time.AfterFunc(20*time.Millisecond, cancel)
	defer timer.Stop()
	err := client.WatchSSE(ctx, nil, func(Event) error {
		t.Fatalf("handler should not receive an event")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WatchSSE(cancel) = %v, want context.Canceled", err)
	}
}

func TestWatchSSEReturnsDeadlineExceeded(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := client.WatchSSE(ctx, nil, func(Event) error {
		t.Fatalf("handler should not receive an event")
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WatchSSE(deadline) = %v, want context.DeadlineExceeded", err)
	}
}

func TestCheckHealth(t *testing.T) {
	tests := []struct {
		name      string
		liveBody  string
		readyBody string
		readyCode int
		want      EngineHealth
		wantError string
	}{
		{name: "ready", liveBody: `{"status":"live"}`, readyBody: `{"status":"ready"}`, readyCode: http.StatusOK, want: EngineHealth{Live: true, Ready: true}},
		{name: "unavailable", liveBody: `{"status":"live"}`, readyBody: `{"status":"unavailable"}`, readyCode: http.StatusServiceUnavailable, want: EngineHealth{Live: true}},
		{name: "invalid liveness", liveBody: `{"status":"wrong"}`, readyBody: `{"status":"ready"}`, readyCode: http.StatusOK, wantError: `unexpected status "wrong"`},
		{name: "invalid readiness", liveBody: `{"status":"live"}`, readyBody: `{`, readyCode: http.StatusOK, wantError: "decode response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/health/live":
					_, _ = w.Write([]byte(tt.liveBody))
				case "/api/health/ready":
					w.WriteHeader(tt.readyCode)
					_, _ = w.Write([]byte(tt.readyBody))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			health, err := testAPIClient(server, "token").CheckHealth(t.Context())
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("CheckHealth() error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil || health == nil || *health != tt.want {
				t.Fatalf("CheckHealth() = %#v, %v, want %#v", health, err, tt.want)
			}
		})
	}
}

func TestCheckHealthSupportsConcurrentCalls(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/api/health/live":
			_, _ = w.Write([]byte(`{"status":"live"}`))
		case "/api/health/ready":
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testAPIClient(server, "token")
	const callers = 32
	var wg sync.WaitGroup
	errorsCh := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			health, err := client.CheckHealth(t.Context())
			if err != nil {
				errorsCh <- err
				return
			}
			if health == nil || !health.Live || !health.Ready {
				errorsCh <- fmt.Errorf("unexpected health: %#v", health)
			}
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	if got := requests.Load(); got != callers*2 {
		t.Fatalf("request count = %d, want %d", got, callers*2)
	}
}

func testAPIClient(server *httptest.Server, token string) *Client {
	engine := testAPIEngine(server)
	return &Client{
		EngineURL: &engine,
		Token:     &token,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			MaxVersion:         tls.VersionTLS12,
		},
	}
}

func testAPIEngine(server *httptest.Server) string {
	return strings.TrimPrefix(server.URL, "https://")
}
