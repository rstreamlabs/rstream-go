// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
	client := &Client{Transport: &Transport{ProxyHTTPHeaders: headers, TLSProxyConfig: tlsCfg, ForceIPv4: BoolPtr(true)}}
	dialer, ok := client.apiDialer().(*Transport)
	if !ok {
		t.Fatalf("apiDialer() returned %T, want *Transport", client.apiDialer())
	}
	headers["X-Trace"] = "mutated"
	tlsCfg.ServerName = "mutated.example.com"
	if dialer.ProxyHTTPHeaders["X-Trace"] != "source" {
		t.Fatalf("proxy headers should be cloned, got %#v", dialer.ProxyHTTPHeaders)
	}
	if dialer.TLSProxyConfig == tlsCfg || dialer.TLSProxyConfig.ServerName != "proxy.example.com" {
		t.Fatalf("TLS proxy config should be cloned, got %#v", dialer.TLSProxyConfig)
	}
	if dialer.ForceIPv4 == nil || !*dialer.ForceIPv4 {
		t.Fatalf("ForceIPv4 not preserved")
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
	if dialer.ProxyHTTP == nil || *dialer.ProxyHTTP != "https://masque.example.com:443" || dialer.ProxySOCKS5 == nil || *dialer.ProxySOCKS5 != "socks5://socks.example.com:1080" {
		t.Fatalf("proxy settings not preserved: %#v", dialer)
	}
	if dialer.ProxyHTTPHeaders["X-Trace"] != "source" {
		t.Fatalf("proxy headers should be cloned, got %#v", dialer.ProxyHTTPHeaders)
	}
	if dialer.TLSProxyConfig == tlsCfg || dialer.TLSProxyConfig.ServerName != "proxy.example.com" {
		t.Fatalf("TLS proxy config should be cloned, got %#v", dialer.TLSProxyConfig)
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
	got, err := conn.Read(nil)
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
	got, err := conn.Read(nil)
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
