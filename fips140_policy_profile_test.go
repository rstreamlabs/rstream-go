//go:build rstream_fips

// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestFIPSProfileAcceptsQUICTransport(t *testing.T) {
	if _, err := NewClient(ClientOptions{
		Engine:    "engine.example:443",
		Transport: &QUICTransport{},
	}); err != nil {
		t.Fatalf("NewClient() rejected QUIC transport: %v", err)
	}

	_, err := (&QUICTransport{}).Dial(t.Context(), "engine.example:443", &tls.Config{MaxVersion: tls.VersionTLS12})
	if err == nil || !strings.Contains(err.Error(), "QUIC requires TLS 1.3") {
		t.Fatalf("QUICTransport.Dial() error = %v, want TLS 1.3 requirement", err)
	}
}

func TestFIPSProfileRejectsProxiedQUICTransport(t *testing.T) {
	proxy := "http://proxy.example:8080"
	_, err := NewClient(ClientOptions{
		Engine:    "engine.example:443",
		Transport: &QUICTransport{ProxyHTTP: &proxy},
	})
	if err == nil || !strings.Contains(err.Error(), "proxied QUIC transport") {
		t.Fatalf("NewClient() error = %v, want proxied QUIC rejection", err)
	}
}

func TestFIPSProfileRejectsUnsafeTLSConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *tls.Config
		want   string
	}{
		{name: "certificate verification disabled", config: &tls.Config{InsecureSkipVerify: true}, want: "cannot disable certificate verification"},
		{name: "TLS 1.1", config: &tls.Config{MaxVersion: tls.VersionTLS11}, want: "maximum version must be TLS 1.2"},
		{name: "ECH", config: &tls.Config{EncryptedClientHelloConfigList: []byte{1}}, want: "cannot enable ECH"},
		{name: "X25519", config: &tls.Config{CurvePreferences: []tls.CurveID{tls.X25519}}, want: "curve X25519 is not approved"},
		{name: "ChaCha20", config: &tls.Config{CipherSuites: []uint16{tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256}}, want: "cipher suite"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewClient(ClientOptions{
				Engine:          "engine.example:443",
				Transport:       &Transport{},
				TLSClientConfig: test.config,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewClient() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestFIPSProfileAutoTransportPrefersQUIC(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	var tlsCalls atomic.Int32
	var quicCalls atomic.Int32
	fallbackDelay := time.Hour
	transport := &AutoTransport{
		tlsDialer: fipsTestDialer{dial: func(context.Context, string, *tls.Config) (net.Conn, error) {
			tlsCalls.Add(1)
			return client, nil
		}},
		quicDialer: fipsTestDialer{dial: func(context.Context, string, *tls.Config) (net.Conn, error) {
			quicCalls.Add(1)
			return client, nil
		}},
		FallbackDelay: &fallbackDelay,
	}
	conn, err := transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if conn != client {
		t.Fatalf("AutoTransport.Dial() connection = %T, want QUIC test connection", conn)
	}
	if tlsCalls.Load() != 0 || quicCalls.Load() != 1 {
		t.Fatalf("transport calls: TLS=%d QUIC=%d", tlsCalls.Load(), quicCalls.Load())
	}
	if mode := transport.SelectedMode(); mode != TunnelTransportModeQUIC {
		t.Fatalf("AutoTransport.SelectedMode() = %q, want %q", mode, TunnelTransportModeQUIC)
	}
}

func TestFIPSProfileRejectsExcludedTunnelFeatures(t *testing.T) {
	tests := []struct {
		name  string
		props TunnelProperties
		want  string
	}{
		{name: "datagram", props: TunnelProperties{Type: TunnelTypePtr(TunnelTypeDatagram)}, want: "datagram tunnels"},
		{name: "DTLS", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolDTLS)}, want: "DTLS tunnels"},
		{name: "WebTTY stream", props: TunnelProperties{Type: TunnelTypePtr(TunnelTypeBytestream), Protocol: ProtocolPtr(ProtocolWebTTY)}, want: "WebTTY transports other than WebTransport"},
		{name: "HTTP3 without HTTP", props: TunnelProperties{HTTPVersion: HTTPVersionPtr(HTTP3)}, want: "published HTTP/3 tunnels"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateFIPSTunnelProperties(test.props)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateFIPSTunnelProperties() error = %v, want %q", err, test.want)
			}
		})
	}
	for _, props := range []TunnelProperties{
		{Type: TunnelTypePtr(TunnelTypeDatagram), Protocol: ProtocolPtr(ProtocolQUIC)},
		{Type: TunnelTypePtr(TunnelTypeDatagram), Protocol: ProtocolPtr(ProtocolHTTP), HTTPVersion: HTTPVersionPtr(HTTP3)},
		{Type: TunnelTypePtr(TunnelTypeDatagram), Protocol: ProtocolPtr(ProtocolWebTTY)},
	} {
		if err := validateFIPSTunnelProperties(props); err != nil {
			t.Fatalf("validated QUIC tunnel rejected: %v", err)
		}
	}
}

func TestFIPSCompatiblePacketDialRequiresApprovedProperties(t *testing.T) {
	client := newTestClientWithDialer(newQueuedDialer(0))
	if _, err := client.PacketDial(t.Context(), Addr{IdOrName: "datagrams"}); err == nil || !strings.Contains(err.Error(), "PacketDialWithProperties") {
		t.Fatalf("PacketDial() error = %v, want explicit-property requirement", err)
	}

	for _, test := range []struct {
		name  string
		props TunnelProperties
		want  string
	}{
		{name: "missing type", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolQUIC)}, want: "non-datagram packet dialing"},
		{name: "generic datagram", props: TunnelProperties{Type: TunnelTypePtr(TunnelTypeDatagram)}, want: "datagram tunnels"},
		{name: "DTLS", props: TunnelProperties{Type: TunnelTypePtr(TunnelTypeDatagram), Protocol: ProtocolPtr(ProtocolDTLS)}, want: "datagram tunnels"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.PacketDialWithProperties(t.Context(), Addr{IdOrName: "datagrams"}, test.props); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("PacketDialWithProperties() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestFIPSCompatiblePacketDialAcceptsReviewedProfiles(t *testing.T) {
	for _, test := range []struct {
		name  string
		props TunnelProperties
	}{
		{name: "QUIC", props: TunnelProperties{Type: TunnelTypePtr(TunnelTypeDatagram), Protocol: ProtocolPtr(ProtocolQUIC)}},
		{name: "HTTP3", props: TunnelProperties{Type: TunnelTypePtr(TunnelTypeDatagram), Protocol: ProtocolPtr(ProtocolHTTP), HTTPVersion: HTTPVersionPtr(HTTP3)}},
		{name: "WebTTY WebTransport", props: TunnelProperties{Type: TunnelTypePtr(TunnelTypeDatagram), Protocol: ProtocolPtr(ProtocolWebTTY)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dialer := newQueuedDialer(1)
			dialer.enqueue(func(conn net.Conn) error {
				reader := bufio.NewReader(conn)
				writer := bufio.NewWriter(conn)
				if _, err := expectStreamReq(reader, "datagrams", "token"); err != nil {
					return err
				}
				return writeStreamRsp(writer, "stream-1")
			})
			client := newTestClientWithDialer(dialer)
			client.Transport = &AutoTransport{selected: dialer, selectedMode: TunnelTransportModeTLS}
			conn, err := client.PacketDialWithProperties(t.Context(), Addr{IdOrName: "datagrams"}, test.props)
			if err != nil {
				t.Fatalf("PacketDialWithProperties() error = %v", err)
			}
			if err := conn.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			dialer.wait(t, 1)
		})
	}
}

func TestFIPSProfileRejectsTURN(t *testing.T) {
	_, err := CreateTURNCredentials(t.Context(), CreateTURNCredentialsOptions{})
	if err == nil || !strings.Contains(err.Error(), "TURN support is not available") {
		t.Fatalf("CreateTURNCredentials() error = %v, want unavailable TURN support", err)
	}
}

func TestFIPSProfileTLSRoundTrip(t *testing.T) {
	certificate, roots := fipsTestCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates:     []tls.Certificate{certificate},
		MinVersion:       tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.CurveP256},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer conn.Close()
		payload := make([]byte, 4)
		if _, readErr := io.ReadFull(conn, payload); readErr != nil {
			serverErr <- readErr
			return
		}
		_, writeErr := conn.Write(payload)
		serverErr <- writeErr
	}()

	conn, err := (&Transport{}).Dial(t.Context(), listener.Addr().String(), &tls.Config{
		RootCAs:          roots,
		ServerName:       "localhost",
		MinVersion:       tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.CurveP256},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "ping" {
		t.Fatalf("TLS reply = %q, want ping", reply)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestFIPSProfileQUICRoundTrip(t *testing.T) {
	certificate, roots := fipsTestCertificate(t)
	listener, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates:     []tls.Certificate{certificate},
		MinVersion:       tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.CurveP256},
		NextProtos:       []string{"rstrm/1"},
	}, &quic.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept(t.Context())
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		stream, acceptErr := conn.AcceptStream(t.Context())
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		payload := make([]byte, 4)
		if _, readErr := io.ReadFull(stream, payload); readErr != nil {
			serverErr <- readErr
			return
		}
		_, writeErr := stream.Write(payload)
		serverErr <- writeErr
	}()

	transport := &QUICTransport{}
	t.Cleanup(func() { _ = transport.Close() })
	conn, err := transport.Dial(t.Context(), listener.Addr().String(), &tls.Config{
		RootCAs:          roots,
		ServerName:       "localhost",
		MinVersion:       tls.VersionTLS13,
		MaxVersion:       tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.CurveP256},
		NextProtos:       []string{"rstrm/1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatal(err)
	}
	if string(reply) != "ping" {
		t.Fatalf("QUIC reply = %q, want ping", reply)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func fipsTestCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); !ok {
		t.Fatal("failed to add test certificate to trust pool")
	}
	return certificate, roots
}

type fipsTestDialer struct {
	dial func(context.Context, string, *tls.Config) (net.Conn, error)
}

func (d fipsTestDialer) Dial(ctx context.Context, addr string, config *tls.Config) (net.Conn, error) {
	return d.dial(ctx, addr, config)
}
