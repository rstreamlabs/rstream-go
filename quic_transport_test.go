// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	masque "github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"
)

func TestDatagramChannelIDFromStreamID(t *testing.T) {
	tests := map[string]string{
		"01020304abcd000000000001":             "01020304abcd000000000001",
		"ABCDEF010203040506070809":             "abcdef010203040506070809",
		"01020304-0000-0000-0000-000000000000": "010203040000000000000000",
	}
	for input, want := range tests {
		got, err := datagramChannelIDFromStreamID(input)
		if err != nil {
			t.Fatalf("datagramChannelIDFromStreamID(%q) error = %v", input, err)
		}
		if got.String() != want {
			t.Fatalf("datagramChannelIDFromStreamID(%q) = %s, want %s", input, got.String(), want)
		}
	}
	for _, input := range []string{"short", "zzzzzzzz0000000000000000"} {
		if _, err := datagramChannelIDFromStreamID(input); err == nil {
			t.Fatalf("datagramChannelIDFromStreamID(%q) error = nil, want validation error", input)
		}
	}
}

func TestQUICDatagramChannelWritePrefixesChannelID(t *testing.T) {
	provider := &recordingDatagramProvider{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	channel := &quicDatagramChannel{
		channelID: mustDatagramChannelID(t, "01020304abcd000000000001"),
		provider:  provider,
		laddr:     stubNetAddr("local"),
		raddr:     stubNetAddr("remote"),
		recvCh:    make(chan []byte, 1),
		ctx:       ctx,
		cancel:    cancel,
	}
	n, err := channel.WriteTo([]byte("payload"), stubNetAddr("remote"))
	if err != nil || n != len("payload") {
		t.Fatalf("WriteTo() = %d, %v", n, err)
	}
	if len(provider.sent) != 1 {
		t.Fatalf("expected one datagram, got %d", len(provider.sent))
	}
	got := provider.sent[0]
	if string(got[:datagramChannelIDSize]) != string(channel.channelID[:]) || string(got[datagramChannelIDSize:]) != "payload" {
		t.Fatalf("unexpected datagram frame: %#v", got)
	}
	provider.err = errors.New("send failed")
	if _, err := channel.WriteTo([]byte("payload"), stubNetAddr("remote")); err == nil {
		t.Fatalf("expected provider error")
	}
}

func mustDatagramChannelID(t *testing.T, streamID string) datagramChannelID {
	t.Helper()
	id, err := datagramChannelIDFromStreamID(streamID)
	if err != nil {
		t.Fatalf("datagramChannelIDFromStreamID(%q) error = %v", streamID, err)
	}
	return id
}

func TestQUICDatagramChannelReadAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	channel := &quicDatagramChannel{
		provider: &recordingDatagramProvider{},
		laddr:    stubNetAddr("local"),
		raddr:    stubNetAddr("remote"),
		recvCh:   make(chan []byte, 1),
		ctx:      ctx,
		cancel:   cancel,
	}
	channel.recvCh <- []byte("packet")
	buf := make([]byte, 16)
	n, addr, err := channel.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "packet" || addr != stubNetAddr("local") {
		t.Fatalf("ReadFrom() = %q from %v", buf[:n], addr)
	}
	if channel.LocalAddr() != stubNetAddr("local") {
		t.Fatalf("LocalAddr() = %v, want local", channel.LocalAddr())
	}
	if err := channel.SetDeadline(time.Now()); !errors.Is(err, errDatagramDeadlineUnsupported) {
		t.Fatalf("SetDeadline() error = %v, want errDatagramDeadlineUnsupported", err)
	}
	if err := channel.SetReadDeadline(time.Now()); !errors.Is(err, errDatagramDeadlineUnsupported) {
		t.Fatalf("SetReadDeadline() error = %v, want errDatagramDeadlineUnsupported", err)
	}
	if err := channel.SetWriteDeadline(time.Now()); !errors.Is(err, errDatagramDeadlineUnsupported) {
		t.Fatalf("SetWriteDeadline() error = %v, want errDatagramDeadlineUnsupported", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := channel.ReadFrom(buf); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ReadFrom() after close error = %v, want net.ErrClosed", err)
	}
}

func TestQUICTransportErrorsBeforeConnection(t *testing.T) {
	transport := &QUICTransport{}
	if err := transport.SendDatagram([]byte("payload")); err == nil || !strings.Contains(err.Error(), "not established") {
		t.Fatalf("SendDatagram() error = %v, want not established", err)
	}
	if _, err := transport.ReceiveDatagram(t.Context()); err == nil || !strings.Contains(err.Error(), "not established") {
		t.Fatalf("ReceiveDatagram() error = %v, want not established", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() without connection error = %v", err)
	}
	localAddr := "not-an-ip"
	transport.LocalAddr = &localAddr
	if _, err := transport.Dial(t.Context(), "127.0.0.1:443", &tls.Config{}); err == nil || !strings.Contains(err.Error(), "failed to parse local address") {
		t.Fatalf("Dial() error = %v, want local address parse error", err)
	}
}

func TestQUICTransportCloseInvalidatesInFlightConnectGeneration(t *testing.T) {
	transport := &QUICTransport{}
	before := transport.closeGeneration
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() without connection error = %v", err)
	}
	if transport.closeGeneration != before+1 {
		t.Fatalf("Close() should invalidate in-flight QUIC connection attempts, generation=%d before=%d", transport.closeGeneration, before)
	}
	localAddr := "not-an-ip"
	transport.LocalAddr = &localAddr
	if _, err := transport.Dial(t.Context(), "127.0.0.1:443", &tls.Config{}); err == nil || !strings.Contains(err.Error(), "failed to parse local address") {
		t.Fatalf("Dial() after idle Close() error = %v, want local address validation", err)
	}
}

func TestQUICTransportOriginBindsAddrAndTLSIdentity(t *testing.T) {
	origin, err := quicTransportOrigin("Example.COM:443", &tls.Config{
		ServerName: "Tunnel.Example.COM",
		NextProtos: []string{
			"h3",
			"rstream",
		},
	})
	if err != nil {
		t.Fatalf("quicTransportOrigin() error = %v", err)
	}
	if origin != "example.com:443|tunnel.example.com|h3,rstream" {
		t.Fatalf("unexpected origin: %q", origin)
	}
	other, err := quicTransportOrigin("example.com:443", &tls.Config{ServerName: "other.example.com"})
	if err != nil {
		t.Fatalf("quicTransportOrigin() error = %v", err)
	}
	if other == origin {
		t.Fatalf("different TLS identities should not share QUIC origin")
	}
}

func TestQUICTransportRejectsAmbiguousProxy(t *testing.T) {
	transport := &QUICTransport{
		ProxyHTTP:   StringPtr("https://masque.example.com"),
		ProxySOCKS5: StringPtr("socks5://socks.example.com:1080"),
	}
	_, err := transport.Dial(t.Context(), "127.0.0.1:443", &tls.Config{InsecureSkipVerify: true})
	if err == nil || !strings.Contains(err.Error(), "only one proxy transport") {
		t.Fatalf("Dial() error = %v, want ambiguous proxy validation", err)
	}
}

func TestQUICTransportMASQUEProxyTemplate(t *testing.T) {
	transport := &QUICTransport{ProxyHTTP: StringPtr("https://masque.example.com")}
	tpl, tlsCfg, err := transport.masqueProxyTemplate(t.Context(), "https://masque.example.com", dnsResolverConfig{})
	if err != nil {
		t.Fatalf("masqueProxyTemplate() error = %v", err)
	}
	expanded, err := tpl.Expand(uritemplate.Values{
		"target_host": uritemplate.String("target.example.com"),
		"target_port": uritemplate.String("443"),
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if expanded != "https://masque.example.com:443/.well-known/masque/udp/target.example.com/443/" {
		t.Fatalf("expanded template = %q", expanded)
	}
	if tlsCfg.ServerName != "masque.example.com" || !hasNextProto(tlsCfg.NextProtos, "h3") {
		t.Fatalf("unexpected MASQUE TLS config: %#v", tlsCfg)
	}
}

func TestQUICTransportMASQUEProxyRejectsHTTPHeaders(t *testing.T) {
	transport := &QUICTransport{
		ProxyHTTP:        StringPtr("https://masque.example.com"),
		ProxyHTTPHeaders: map[string]string{"X-Trace": "abc"},
	}
	_, _, _, err := transport.connectHTTPProxy(t.Context(), "https://masque.example.com", "target.example.com:443", nil, nil, dnsResolverConfig{}, "udp4", nil)
	if err == nil || !strings.Contains(err.Error(), "custom HTTP proxy headers") {
		t.Fatalf("connectHTTPProxy() error = %v, want header validation", err)
	}
}

func TestQUICTransportSOCKS5ProxyToQUICServer(t *testing.T) {
	server := newTestQUICEchoServer(t)
	defer server.close()
	proxy := newTestSOCKS5UDPProxy(t)
	defer proxy.close()
	transport := &QUICTransport{ProxySOCKS5: StringPtr("socks5://" + proxy.addr)}
	defer transport.Close()
	conn, err := transport.Dial(t.Context(), server.addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
	if err != nil {
		t.Fatalf("Dial() through SOCKS5 UDP proxy error = %v", err)
	}
	defer conn.Close()
	assertQUICEchoConn(t, conn)
}

func TestQUICTransportSOCKS5ProxyPreservesLogicalHostname(t *testing.T) {
	server := newTestQUICEchoServer(t)
	defer server.close()
	_, port, err := net.SplitHostPort(server.addr)
	if err != nil {
		t.Fatalf("split QUIC server address: %v", err)
	}
	proxy := newTestSOCKS5UDPProxyWithOptions(t, testSOCKS5UDPProxyOptions{
		assertTarget: func(target socks5Address) {
			if target.host != "localhost" || strconv.Itoa(target.port) != port {
				t.Errorf("SOCKS5 UDP target = %#v, want localhost:%s", target, port)
			}
		},
	})
	defer proxy.close()
	transport := &QUICTransport{ProxySOCKS5: StringPtr("socks5://" + proxy.addr)}
	defer transport.Close()
	conn, err := transport.Dial(t.Context(), net.JoinHostPort("localhost", port), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
	if err != nil {
		t.Fatalf("Dial() through SOCKS5 UDP proxy error = %v", err)
	}
	defer conn.Close()
	assertQUICEchoConn(t, conn)
}

func TestQUICTransportSOCKS5ProxyRejectsTLSProxyConfig(t *testing.T) {
	transport := &QUICTransport{
		ProxySOCKS5:    StringPtr("socks5://proxy.example.com:1080"),
		TLSProxyConfig: &tls.Config{InsecureSkipVerify: true},
	}
	_, err := transport.Dial(t.Context(), "127.0.0.1:443", &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
	if err == nil || !strings.Contains(err.Error(), "TLS proxy configuration") {
		t.Fatalf("Dial() error = %v, want TLS proxy config validation", err)
	}
}

func TestQUICTransportRejectsStandaloneProxyTLSConfig(t *testing.T) {
	transport := &QUICTransport{TLSProxyConfig: &tls.Config{ServerName: "proxy.local"}}
	_, err := transport.Dial(t.Context(), "127.0.0.1:443", &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
	if err == nil || !strings.Contains(err.Error(), "TLS proxy configuration") {
		t.Fatalf("Dial() error = %v, want TLS proxy config validation", err)
	}
}

func TestQUICTransportMASQUEProxyToQUICServer(t *testing.T) {
	server := newTestQUICEchoServer(t)
	defer server.close()
	proxy := newTestMASQUEProxy(t)
	defer proxy.close()
	transport := &QUICTransport{
		ProxyHTTP:      StringPtr(proxy.url),
		TLSProxyConfig: &tls.Config{InsecureSkipVerify: true},
	}
	defer transport.Close()
	conn, err := transport.Dial(t.Context(), server.addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
	if err != nil {
		t.Fatalf("Dial() through MASQUE proxy error = %v", err)
	}
	defer conn.Close()
	assertQUICEchoConn(t, conn)
}

func TestMASQUEUDPPacketConnReadDeadlineCanBeResetAfterExpiry(t *testing.T) {
	stream := newBlockingMASQUEUDPStream()
	conn := newMASQUEUDPPacketConn(stream, stubNetAddr("local"), stubNetAddr("remote"))
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 16)
	if _, _, err := conn.ReadFrom(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("ReadFrom() error = %v, want deadline exceeded", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() reset error = %v", err)
	}
	stream.datagrams <- []byte{0, 'o', 'k'}
	n, addr, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() after reset error = %v", err)
	}
	if got := string(buf[:n]); got != "ok" {
		t.Fatalf("ReadFrom() payload = %q", got)
	}
	if addr != stubNetAddr("remote") {
		t.Fatalf("ReadFrom() addr = %v, want remote", addr)
	}
}

func TestMASQUEUDPPacketConnReadDeadlineCanBeClearedAfterExpiry(t *testing.T) {
	stream := newBlockingMASQUEUDPStream()
	conn := newMASQUEUDPPacketConn(stream, stubNetAddr("local"), stubNetAddr("remote"))
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 16)
	if _, _, err := conn.ReadFrom(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("ReadFrom() error = %v, want deadline exceeded", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("SetReadDeadline() clear error = %v", err)
	}
	stream.datagrams <- []byte{0, 'o', 'k'}
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() after clear error = %v", err)
	}
	if got := string(buf[:n]); got != "ok" {
		t.Fatalf("ReadFrom() payload = %q", got)
	}
}

func TestQUICTransportMASQUEProxyHonorsCustomDNSAndLocalAddress(t *testing.T) {
	resolverAddr := startDNSResolutionTestServer(t, dnsAnswerAllA("127.0.0.1"))
	server := newTestQUICEchoServer(t)
	defer server.close()
	_, serverPort, err := net.SplitHostPort(server.addr)
	if err != nil {
		t.Fatalf("split QUIC server address: %v", err)
	}
	target := net.JoinHostPort("engine.example.test", serverPort)
	proxy := newTestMASQUEProxyWithOptions(t, testMASQUEProxyOptions{
		publicHostname: "masque.example.test",
		assertTarget: func(target string) {
			if target != net.JoinHostPort("127.0.0.1", serverPort) {
				t.Fatalf("MASQUE target = %q, want custom-DNS resolved target", target)
			}
		},
	})
	defer proxy.close()
	localAddr := "127.0.0.1"
	transport := &QUICTransport{
		LocalAddr:      &localAddr,
		DNSOverride:    &resolverAddr,
		ForceIPv4:      BoolPtr(true),
		ProxyHTTP:      StringPtr(proxy.url),
		TLSProxyConfig: &tls.Config{InsecureSkipVerify: true},
	}
	defer transport.Close()
	conn, err := transport.Dial(t.Context(), target, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
	if err != nil {
		t.Fatalf("Dial() through DNS-resolved MASQUE proxy error = %v", err)
	}
	defer conn.Close()
	assertQUICEchoConn(t, conn)
	transport.mu.Lock()
	proxiedLocal := transport.pconn.LocalAddr().String()
	transport.mu.Unlock()
	if !strings.HasPrefix(proxiedLocal, "127.0.0.1:") {
		t.Fatalf("MASQUE local address = %q, want 127.0.0.1 bind", proxiedLocal)
	}
}

func TestQUICTransportSOCKS5ProxyHonorsCustomDNSAndLocalAddress(t *testing.T) {
	resolverAddr := startDNSResolutionTestServer(t, dnsAnswerAllA("127.0.0.1"))
	server := newTestQUICEchoServer(t)
	defer server.close()
	_, serverPort, err := net.SplitHostPort(server.addr)
	if err != nil {
		t.Fatalf("split QUIC server address: %v", err)
	}
	target := net.JoinHostPort("engine.example.test", serverPort)
	proxy := newTestSOCKS5UDPProxyWithOptions(t, testSOCKS5UDPProxyOptions{
		assertTarget: func(target socks5Address) {
			if target.host != "127.0.0.1" || strconv.Itoa(target.port) != serverPort {
				t.Errorf("SOCKS5 UDP target = %#v, want custom-DNS resolved target", target)
			}
		},
	})
	defer proxy.close()
	_, proxyPort, err := net.SplitHostPort(proxy.addr)
	if err != nil {
		t.Fatalf("split SOCKS5 proxy address: %v", err)
	}
	proxyURL := "socks5://" + net.JoinHostPort("proxy.example.test", proxyPort)
	localAddr := "127.0.0.1"
	transport := &QUICTransport{
		LocalAddr:   &localAddr,
		DNSOverride: &resolverAddr,
		ForceIPv4:   BoolPtr(true),
		ProxySOCKS5: StringPtr(proxyURL),
	}
	defer transport.Close()
	conn, err := transport.Dial(t.Context(), target, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{testQUICALPN}})
	if err != nil {
		t.Fatalf("Dial() through DNS-resolved SOCKS5 proxy error = %v", err)
	}
	defer conn.Close()
	assertQUICEchoConn(t, conn)
	transport.mu.Lock()
	proxiedLocal := transport.pconn.LocalAddr().String()
	transport.mu.Unlock()
	if !strings.HasPrefix(proxiedLocal, "127.0.0.1:") {
		t.Fatalf("SOCKS5 UDP local address = %q, want 127.0.0.1 bind", proxiedLocal)
	}
}

func TestQUICDatagramListenerAcceptAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	listener := &quicDatagramListener{
		conns:  make(chan net.PacketConn, 1),
		ctx:    ctx,
		cancel: cancel,
		laddr:  stubNetAddr("listener"),
	}
	conn := newFakePacketConn(stubNetAddr("conn"))
	listener.conns <- conn
	got, addr, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if got != conn || addr != conn.LocalAddr() {
		t.Fatalf("Accept() = %v, %v", got, addr)
	}
	if listener.Addr() != stubNetAddr("listener") {
		t.Fatalf("Addr() = %v", listener.Addr())
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept() after close error = %v, want net.ErrClosed", err)
	}
}

type recordingDatagramProvider struct {
	sent [][]byte
	err  error
}

func (p *recordingDatagramProvider) SendDatagram(data []byte) error {
	if p.err != nil {
		return p.err
	}
	p.sent = append(p.sent, append([]byte(nil), data...))
	return nil
}

func (p *recordingDatagramProvider) ReceiveDatagram(context.Context) ([]byte, error) {
	return nil, errors.New("not implemented")
}

type blockingMASQUEUDPStream struct {
	datagrams chan []byte
	closed    chan struct{}
	once      sync.Once
	sent      [][]byte
}

func newBlockingMASQUEUDPStream() *blockingMASQUEUDPStream {
	return &blockingMASQUEUDPStream{
		datagrams: make(chan []byte, 1),
		closed:    make(chan struct{}),
	}
}

func (s *blockingMASQUEUDPStream) Read([]byte) (int, error) {
	<-s.closed
	return 0, io.EOF
}

func (s *blockingMASQUEUDPStream) Write(p []byte) (int, error) {
	return len(p), nil
}

func (s *blockingMASQUEUDPStream) Close() error {
	s.once.Do(func() {
		close(s.closed)
	})
	return nil
}

func (s *blockingMASQUEUDPStream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case data := <-s.datagrams:
		return data, nil
	case <-s.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *blockingMASQUEUDPStream) SendDatagram(data []byte) error {
	s.sent = append(s.sent, append([]byte(nil), data...))
	return nil
}

func (s *blockingMASQUEUDPStream) CancelRead(quic.StreamErrorCode) {
	_ = s.Close()
}

const testQUICALPN = "rstream-test-quic"

type testQUICEchoServer struct {
	addr  string
	close func()
}

func newTestQUICEchoServer(t *testing.T) testQUICEchoServer {
	t.Helper()
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen QUIC UDP: %v", err)
	}
	transport := &quic.Transport{Conn: udpConn}
	listener, err := transport.Listen(testQUICServerTLSConfig(t, testQUICALPN), &quic.Config{EnableDatagrams: true, InitialPacketSize: 1200})
	if err != nil {
		_ = udpConn.Close()
		t.Fatalf("listen QUIC: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept(context.Background())
			if err != nil {
				return
			}
			go func(conn *quic.Conn) {
				stream, err := conn.AcceptStream(context.Background())
				if err == nil {
					_, _ = io.Copy(stream, stream)
					_ = stream.Close()
				}
				_ = conn.CloseWithError(0, "done")
			}(conn)
		}
	}()
	return testQUICEchoServer{
		addr: udpConn.LocalAddr().String(),
		close: func() {
			_ = listener.Close()
			_ = transport.Close()
			_ = udpConn.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("QUIC server goroutine did not exit")
			}
		},
	}
}

func testQUICServerTLSConfig(t *testing.T, alpn string) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test certificate: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse test certificate: %v", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{alpn}}
}

func assertQUICEchoConn(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write QUIC stream: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read QUIC stream: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo payload = %q", buf)
	}
}

type testSOCKS5UDPProxy struct {
	addr  string
	close func()
}

type testSOCKS5UDPProxyOptions struct {
	assertTarget func(socks5Address)
}

func newTestSOCKS5UDPProxy(t *testing.T) testSOCKS5UDPProxy {
	return newTestSOCKS5UDPProxyWithOptions(t, testSOCKS5UDPProxyOptions{})
}

func newTestSOCKS5UDPProxyWithOptions(t *testing.T, options testSOCKS5UDPProxyOptions) testSOCKS5UDPProxy {
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
			t.Errorf("read SOCKS5 UDP request header: %v", err)
			return
		}
		if _, err := socks5ReadAddress(conn, header[3]); err != nil {
			t.Errorf("read SOCKS5 UDP request address: %v", err)
			return
		}
		if header[1] != socks5CmdUDP {
			t.Errorf("SOCKS5 command = %d, want UDP ASSOCIATE", header[1])
			return
		}
		relay, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Errorf("listen SOCKS5 UDP relay: %v", err)
			return
		}
		defer relay.Close()
		go func() {
			_, _ = io.Copy(io.Discard, conn)
			_ = relay.Close()
		}()
		port := relay.LocalAddr().(*net.UDPAddr).Port
		_, _ = conn.Write([]byte{socks5Version, socks5StatusSuccess, 0, socks5AddrIPv4, 127, 0, 0, 1, byte(port >> 8), byte(port)})
		relaySOCKS5UDP(t, relay, options.assertTarget)
	}()
	return testSOCKS5UDPProxy{
		addr: listener.Addr().String(),
		close: func() {
			_ = listener.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("SOCKS5 UDP proxy goroutine did not exit")
			}
		},
	}
}

func relaySOCKS5UDP(t *testing.T, relay *net.UDPConn, assertTarget func(socks5Address)) {
	t.Helper()
	buf := make([]byte, socks5MaxUDPPacket)
	var clientAddr *net.UDPAddr
	for {
		n, addr, err := relay.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if clientAddr == nil || socks5SameUDPAddr(addr, clientAddr) {
			clientAddr = addr
			payload, target, err := socks5DecodeUDPDatagram(buf[:n])
			if err != nil {
				t.Errorf("decode SOCKS5 UDP datagram: %v", err)
				return
			}
			if assertTarget != nil {
				assertTarget(target)
			}
			targetAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(target.host, strconv.Itoa(target.port)))
			if err != nil {
				t.Errorf("resolve SOCKS5 UDP target: %v", err)
				return
			}
			if _, err := relay.WriteToUDP(payload, targetAddr); err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				t.Errorf("write SOCKS5 UDP target: %v", err)
				return
			}
			continue
		}
		if clientAddr == nil {
			continue
		}
		source := socks5Address{host: addr.IP.String(), port: addr.Port}
		packet, err := socks5BuildUDPDatagram(source, buf[:n])
		if err != nil {
			t.Errorf("build SOCKS5 UDP datagram: %v", err)
			return
		}
		if _, err := relay.WriteToUDP(packet, clientAddr); err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			t.Errorf("write SOCKS5 UDP client: %v", err)
			return
		}
	}
}

type testMASQUEProxy struct {
	url   string
	close func()
}

type testMASQUEProxyOptions struct {
	publicHostname string
	assertTarget   func(string)
}

func newTestMASQUEProxy(t *testing.T) testMASQUEProxy {
	return newTestMASQUEProxyWithOptions(t, testMASQUEProxyOptions{})
}

func newTestMASQUEProxyWithOptions(t *testing.T, options testMASQUEProxyOptions) testMASQUEProxy {
	t.Helper()
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen MASQUE UDP: %v", err)
	}
	publicAddr := udpConn.LocalAddr().String()
	if options.publicHostname != "" {
		_, port, err := net.SplitHostPort(publicAddr)
		if err != nil {
			_ = udpConn.Close()
			t.Fatalf("split MASQUE proxy address: %v", err)
		}
		publicAddr = net.JoinHostPort(options.publicHostname, port)
	}
	template := uritemplate.MustNew("https://" + publicAddr + "/masque/{target_host}/{target_port}/")
	mux := http.NewServeMux()
	proxy := &masque.Proxy{}
	server := &http3.Server{
		TLSConfig:       testQUICServerTLSConfig(t, http3.NextProtoH3),
		QUICConfig:      &quic.Config{EnableDatagrams: true},
		EnableDatagrams: true,
		Handler:         mux,
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		req, err := masque.ParseRequest(r, template)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if options.assertTarget != nil {
			options.assertTarget(req.Target)
		}
		if err := proxy.Proxy(w, req); err != nil {
			t.Errorf("MASQUE proxy error: %v", err)
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(udpConn)
	}()
	return testMASQUEProxy{
		url: "https://" + publicAddr + "/masque/{target_host}/{target_port}/",
		close: func() {
			_ = proxy.Close()
			_ = server.Close()
			_ = udpConn.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("MASQUE proxy goroutine did not exit")
			}
		},
	}
}
