// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTransportDialDirectTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	conn, err := (&Transport{}).Dial(t.Context(), strings.TrimPrefix(server.URL, "https://"), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: local\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write HTTP request over TLS conn: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read HTTP response over TLS conn: %v", err)
	}
	_ = resp.Body.Close()
	if conn.RemoteAddr() == nil || conn.LocalAddr() == nil {
		t.Fatalf("expected local and remote addresses")
	}
}

func TestTransportDialRejectsInvalidLocalAddress(t *testing.T) {
	_, err := (&Transport{LocalAddr: StringPtr("not-an-ip")}).Dial(t.Context(), "127.0.0.1:443", nil)
	if err == nil || !strings.Contains(err.Error(), "failed to parse local address") {
		t.Fatalf("expected local address parse error, got %v", err)
	}
}

func TestTransportDialHTTPProxyCONNECT(t *testing.T) {
	proxy := newTestHTTPProxy(t, func(req *http.Request) {
		if req.Method != http.MethodConnect {
			t.Fatalf("proxy method = %s", req.Method)
		}
		if req.Host != "target.example.com:443" {
			t.Fatalf("proxy host = %q", req.Host)
		}
		if got := req.Header.Get("X-Trace"); got != "abc" {
			t.Fatalf("proxy header X-Trace = %q", got)
		}
		if got := req.Header.Get("Proxy-Authorization"); got != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")) {
			t.Fatalf("proxy authorization = %q", got)
		}
	})
	defer proxy.close()
	proxyURL := "http://" + proxy.addr
	conn, err := (&Transport{
		ProxyHTTP:        &proxyURL,
		ProxyUsername:    StringPtr("user"),
		ProxyPassword:    StringPtr("pass"),
		ProxyHTTPHeaders: map[string]string{"X-Trace": "abc"},
	}).Dial(t.Context(), "target.example.com:443", nil)
	if err != nil {
		t.Fatalf("Dial() through proxy error = %v", err)
	}
	defer conn.Close()
}

type testHTTPProxy struct {
	addr  string
	close func()
}

func newTestHTTPProxy(t *testing.T, assert func(*http.Request)) testHTTPProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen proxy: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		req, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			t.Errorf("read CONNECT request: %v", err)
			return
		}
		assert(req)
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		time.Sleep(20 * time.Millisecond)
	}()
	return testHTTPProxy{
		addr: listener.Addr().String(),
		close: func() {
			_ = listener.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("proxy goroutine did not exit")
			}
		},
	}
}

func TestResolveDialAddressRejectsInvalidAddress(t *testing.T) {
	_, err := resolveDialAddress(context.Background(), "missing-port", dnsResolverConfig{override: "127.0.0.1:53"})
	if err == nil || !strings.Contains(err.Error(), "failed to split host:port") {
		t.Fatalf("expected split host:port error, got %v", err)
	}
}
