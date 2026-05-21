// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		if req.RequestURI != "target.example.com:443" {
			t.Fatalf("proxy request URI = %q", req.RequestURI)
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

func TestTransportDialHTTPProxyCONNECTToTLSServer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proxy func(*testing.T) testForwardingHTTPProxy
	}{
		{name: "plain proxy", proxy: newForwardingHTTPProxy},
		{name: "TLS proxy", proxy: newForwardingHTTPSProxy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("ok"))
			}))
			defer server.Close()
			proxy := tc.proxy(t)
			defer proxy.close()
			transport := &Transport{ProxyHTTP: StringPtr(proxy.url)}
			if proxy.tls {
				transport.TLSProxyConfig = &tls.Config{InsecureSkipVerify: true}
			}
			conn, err := transport.Dial(t.Context(), strings.TrimPrefix(server.URL, "https://"), &tls.Config{InsecureSkipVerify: true})
			if err != nil {
				t.Fatalf("Dial() through HTTP proxy error = %v", err)
			}
			defer conn.Close()
			assertTLSHTTPConn(t, conn)
		})
	}
}

func TestTransportDialSOCKS5ProxyCONNECT(t *testing.T) {
	proxy := newTestSOCKS5Proxy(t, testSOCKS5ProxyOptions{
		username: "user",
		password: "pass",
		assert: func(command byte, target socks5Address) {
			if command != socks5CmdConnect {
				t.Fatalf("SOCKS5 command = %d, want CONNECT", command)
			}
			if target.host != "target.example.com" || target.port != 443 {
				t.Fatalf("SOCKS5 target = %#v", target)
			}
		},
	})
	defer proxy.close()
	proxyURL := "socks5://" + proxy.addr
	conn, err := (&Transport{
		ProxySOCKS5:   &proxyURL,
		ProxyUsername: StringPtr("user"),
		ProxyPassword: StringPtr("pass"),
	}).Dial(t.Context(), "target.example.com:443", nil)
	if err != nil {
		t.Fatalf("Dial() through SOCKS5 proxy error = %v", err)
	}
	defer conn.Close()
}

func TestTransportDialSOCKS5ProxyCONNECTToTLSServer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	proxy := newForwardingSOCKS5Proxy(t)
	defer proxy.close()
	proxyURL := "socks5://" + proxy.addr
	conn, err := (&Transport{ProxySOCKS5: StringPtr(proxyURL)}).Dial(t.Context(), strings.TrimPrefix(server.URL, "https://"), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("Dial() through SOCKS5 proxy error = %v", err)
	}
	defer conn.Close()
	assertTLSHTTPConn(t, conn)
}

func TestTransportDialProxyUsesCustomDNSTarget(t *testing.T) {
	resolverAddr := startDNSResolutionTestServer(t, dnsAnswerAllA("127.0.0.1"))
	httpProxy := newTestHTTPProxy(t, func(req *http.Request) {
		if req.Host != "127.0.0.1:443" {
			t.Fatalf("HTTP proxy target = %q, want resolved IP target", req.Host)
		}
	})
	defer httpProxy.close()
	_, httpProxyPort, err := net.SplitHostPort(httpProxy.addr)
	if err != nil {
		t.Fatalf("split HTTP proxy address: %v", err)
	}
	httpProxyURL := "http://" + net.JoinHostPort("proxy.example.test", httpProxyPort)
	httpConn, err := (&Transport{
		DNSOverride: StringPtr(resolverAddr),
		ForceIPv4:   BoolPtr(true),
		ProxyHTTP:   StringPtr(httpProxyURL),
	}).Dial(t.Context(), "engine.example.com:443", nil)
	if err != nil {
		t.Fatalf("Dial() through HTTP proxy with custom DNS error = %v", err)
	}
	defer httpConn.Close()
	socksProxy := newTestSOCKS5Proxy(t, testSOCKS5ProxyOptions{
		assert: func(command byte, target socks5Address) {
			if command != socks5CmdConnect {
				t.Fatalf("SOCKS5 command = %d, want CONNECT", command)
			}
			if target.host != "127.0.0.1" || target.port != 443 {
				t.Fatalf("SOCKS5 target = %#v, want resolved IP target", target)
			}
		},
	})
	defer socksProxy.close()
	_, socksProxyPort, err := net.SplitHostPort(socksProxy.addr)
	if err != nil {
		t.Fatalf("split SOCKS5 proxy address: %v", err)
	}
	socksProxyURL := "socks5://" + net.JoinHostPort("proxy.example.test", socksProxyPort)
	socksConn, err := (&Transport{
		DNSOverride: StringPtr(resolverAddr),
		ForceIPv4:   BoolPtr(true),
		ProxySOCKS5: StringPtr(socksProxyURL),
	}).Dial(t.Context(), "engine.example.com:443", nil)
	if err != nil {
		t.Fatalf("Dial() through SOCKS5 proxy with custom DNS error = %v", err)
	}
	defer socksConn.Close()
}

func TestTransportDialProxyAppliesForcedTargetIPFamily(t *testing.T) {
	httpProxy := newTestHTTPProxy(t, func(req *http.Request) {
		if req.Host != "127.0.0.1:443" {
			t.Fatalf("HTTP proxy target = %q, want IPv4 literal", req.Host)
		}
	})
	defer httpProxy.close()
	httpProxyURL := "http://" + httpProxy.addr
	httpConn, err := (&Transport{
		ForceIPv4: BoolPtr(true),
		ProxyHTTP: StringPtr(httpProxyURL),
	}).Dial(t.Context(), "localhost:443", nil)
	if err != nil {
		t.Fatalf("Dial() through HTTP proxy with forced IPv4 error = %v", err)
	}
	defer httpConn.Close()
	socksProxy := newTestSOCKS5Proxy(t, testSOCKS5ProxyOptions{
		assert: func(command byte, target socks5Address) {
			if command != socks5CmdConnect {
				t.Fatalf("SOCKS5 command = %d, want CONNECT", command)
			}
			if target.host != "127.0.0.1" || target.port != 443 {
				t.Fatalf("SOCKS5 target = %#v, want IPv4 literal", target)
			}
		},
	})
	defer socksProxy.close()
	socksProxyURL := "socks5://" + socksProxy.addr
	socksConn, err := (&Transport{
		ForceIPv4:   BoolPtr(true),
		ProxySOCKS5: StringPtr(socksProxyURL),
	}).Dial(t.Context(), "localhost:443", nil)
	if err != nil {
		t.Fatalf("Dial() through SOCKS5 proxy with forced IPv4 error = %v", err)
	}
	defer socksConn.Close()
}

func TestTransportDialRejectsAmbiguousProxy(t *testing.T) {
	httpProxy := "http://proxy.local:3128"
	socksProxy := "socks5://proxy.local:1080"
	_, err := (&Transport{ProxyHTTP: &httpProxy, ProxySOCKS5: &socksProxy}).Dial(t.Context(), "target.example.com:443", nil)
	if err == nil || !strings.Contains(err.Error(), "only one proxy transport") {
		t.Fatalf("expected ambiguous proxy error, got %v", err)
	}
}

func TestTransportDialRejectsStandaloneProxyTLSConfig(t *testing.T) {
	_, err := (&Transport{TLSProxyConfig: &tls.Config{ServerName: "proxy.local"}}).Dial(t.Context(), "target.example.com:443", nil)
	if err == nil || !strings.Contains(err.Error(), "TLS proxy configuration") {
		t.Fatalf("expected standalone proxy TLS error, got %v", err)
	}
}

func TestTransportDialRejectsInvalidHTTPProxyScheme(t *testing.T) {
	proxy := "socks5://proxy.local:1080"
	_, err := (&Transport{ProxyHTTP: &proxy}).Dial(t.Context(), "target.example.com:443", nil)
	if err == nil || !strings.Contains(err.Error(), `unsupported HTTP proxy scheme "socks5"`) {
		t.Fatalf("expected HTTP proxy scheme error, got %v", err)
	}
}

func TestTransportDialReportsHTTPProxyConnectFailure(t *testing.T) {
	proxy := newTestHTTPProxyResponse(t, "HTTP/1.1 407 Proxy Authentication Required\r\n\r\n")
	defer proxy.close()
	proxyURL := "http://" + proxy.addr
	_, err := (&Transport{ProxyHTTP: &proxyURL}).Dial(t.Context(), "target.example.com:443", nil)
	if err == nil || !strings.Contains(err.Error(), "failed to CONNECT: 407 Proxy Authentication Required") {
		t.Fatalf("expected CONNECT status error, got %v", err)
	}
}

func TestTransportProxyFromEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "socks5://proxy.env:1080")
	t.Setenv("NO_PROXY", "bypass.example.com")
	httpProxy, socksProxy, err := effectiveProxyURLs("", "", BoolPtr(true), "engine.example.com:443")
	if err != nil {
		t.Fatalf("effectiveProxyURLs() error = %v", err)
	}
	if httpProxy != "" || socksProxy != "socks5://proxy.env:1080" {
		t.Fatalf("effectiveProxyURLs() = %q, %q", httpProxy, socksProxy)
	}
	httpProxy, socksProxy, err = effectiveProxyURLs("", "", BoolPtr(true), "bypass.example.com:443")
	if err != nil {
		t.Fatalf("effectiveProxyURLs(NO_PROXY) error = %v", err)
	}
	if httpProxy != "" || socksProxy != "" {
		t.Fatalf("NO_PROXY should bypass proxy, got %q, %q", httpProxy, socksProxy)
	}
}

type testHTTPProxy struct {
	addr  string
	close func()
}

func newTestHTTPProxyResponse(t *testing.T, response string) testHTTPProxy {
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
		if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
			t.Errorf("read CONNECT request: %v", err)
			return
		}
		_, _ = conn.Write([]byte(response))
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

type testForwardingHTTPProxy struct {
	url   string
	tls   bool
	close func()
}

func newForwardingHTTPProxy(t *testing.T) testForwardingHTTPProxy {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(forwardHTTPConnect(t)))
	return testForwardingHTTPProxy{url: server.URL, close: server.Close}
}

func newForwardingHTTPSProxy(t *testing.T) testForwardingHTTPProxy {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(forwardHTTPConnect(t)))
	return testForwardingHTTPProxy{url: server.URL, tls: true, close: server.Close}
}

func forwardHTTPConnect(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			_ = upstream.Close()
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		downstream, _, err := hijacker.Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = downstream.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		proxyConns(downstream, upstream)
	}
}

func assertTLSHTTPConn(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: local\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write HTTP request over proxied TLS conn: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read HTTP response over proxied TLS conn: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %s", resp.Status)
	}
}

func proxyConns(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		_ = a.Close()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		_ = b.Close()
		done <- struct{}{}
	}()
	<-done
}

type testSOCKS5ProxyOptions struct {
	username string
	password string
	assert   func(byte, socks5Address)
}

type testSOCKS5Proxy struct {
	addr  string
	close func()
}

func newTestSOCKS5Proxy(t *testing.T, opts testSOCKS5ProxyOptions) testSOCKS5Proxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen SOCKS5 proxy: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := testSOCKS5Negotiate(conn, opts); err != nil {
			t.Errorf("SOCKS5 negotiation: %v", err)
			return
		}
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Errorf("read SOCKS5 request header: %v", err)
			return
		}
		target, err := socks5ReadAddress(conn, header[3])
		if err != nil {
			t.Errorf("read SOCKS5 request address: %v", err)
			return
		}
		if opts.assert != nil {
			opts.assert(header[1], target)
		}
		_, _ = conn.Write([]byte{socks5Version, socks5StatusSuccess, 0, socks5AddrIPv4, 127, 0, 0, 1, 0, 0})
		time.Sleep(20 * time.Millisecond)
	}()
	return testSOCKS5Proxy{
		addr: listener.Addr().String(),
		close: func() {
			_ = listener.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("SOCKS5 proxy goroutine did not exit")
			}
		},
	}
}

func newForwardingSOCKS5Proxy(t *testing.T) testSOCKS5Proxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen SOCKS5 proxy: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := testSOCKS5Negotiate(conn, testSOCKS5ProxyOptions{}); err != nil {
			t.Errorf("SOCKS5 negotiation: %v", err)
			return
		}
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil {
			t.Errorf("read SOCKS5 request header: %v", err)
			return
		}
		target, err := socks5ReadAddress(conn, header[3])
		if err != nil {
			t.Errorf("read SOCKS5 request address: %v", err)
			return
		}
		if header[1] != socks5CmdConnect {
			t.Errorf("SOCKS5 command = %d, want CONNECT", header[1])
			return
		}
		upstream, err := net.Dial("tcp", net.JoinHostPort(target.host, strconv.Itoa(target.port)))
		if err != nil {
			t.Errorf("dial SOCKS5 upstream: %v", err)
			return
		}
		defer upstream.Close()
		_, _ = conn.Write([]byte{socks5Version, socks5StatusSuccess, 0, socks5AddrIPv4, 127, 0, 0, 1, 0, 0})
		proxyConns(conn, upstream)
	}()
	return testSOCKS5Proxy{
		addr: listener.Addr().String(),
		close: func() {
			_ = listener.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("SOCKS5 proxy goroutine did not exit")
			}
		},
	}
}

func testSOCKS5Negotiate(conn net.Conn, opts testSOCKS5ProxyOptions) error {
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return err
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	if opts.username == "" && opts.password == "" {
		_, err := conn.Write([]byte{socks5Version, socks5NoAuth})
		return err
	}
	_, err := conn.Write([]byte{socks5Version, socks5UserPassAuth})
	if err != nil {
		return err
	}
	authHeader := make([]byte, 2)
	if _, err := io.ReadFull(conn, authHeader); err != nil {
		return err
	}
	username := make([]byte, int(authHeader[1]))
	if _, err := io.ReadFull(conn, username); err != nil {
		return err
	}
	passwordLen := make([]byte, 1)
	if _, err := io.ReadFull(conn, passwordLen); err != nil {
		return err
	}
	password := make([]byte, int(passwordLen[0]))
	if _, err := io.ReadFull(conn, password); err != nil {
		return err
	}
	if string(username) != opts.username || string(password) != opts.password {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return nil
	}
	_, err = conn.Write([]byte{0x01, 0x00})
	return err
}

func TestSOCKS5UDPDatagramCodec(t *testing.T) {
	packet, err := socks5BuildUDPDatagram(socks5Address{host: "target.example.com", port: 443}, []byte("payload"))
	if err != nil {
		t.Fatalf("socks5BuildUDPDatagram() error = %v", err)
	}
	if packet[3] != socks5AddrDomain || int(packet[4]) != len("target.example.com") {
		t.Fatalf("SOCKS5 UDP packet did not encode domain target: %#v", packet[:5])
	}
	payload, addr, err := socks5DecodeUDPDatagram(packet)
	if err != nil {
		t.Fatalf("socks5DecodeUDPDatagram() error = %v", err)
	}
	if string(payload) != "payload" || addr.host != "target.example.com" || addr.port != 443 {
		t.Fatalf("decoded UDP packet = %q %#v", payload, addr)
	}
	ipPacket, err := socks5BuildUDPDatagram(socks5Address{host: "127.0.0.1", port: 53}, []byte("dns"))
	if err != nil {
		t.Fatalf("socks5BuildUDPDatagram(IP) error = %v", err)
	}
	if packetPort := binary.BigEndian.Uint16(ipPacket[8:10]); packetPort != 53 {
		t.Fatalf("encoded UDP port = %d", packetPort)
	}
}

func TestResolveDialAddressRejectsInvalidAddress(t *testing.T) {
	_, err := resolveDialAddress(context.Background(), "missing-port", dnsResolverConfig{override: "127.0.0.1:53"})
	if err == nil || !strings.Contains(err.Error(), "failed to split host:port") {
		t.Fatalf("expected split host:port error, got %v", err)
	}
}
