//go:build rstream_fips

// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFIPSProfileRejectsQUICTransport(t *testing.T) {
	_, err := NewClient(ClientOptions{
		Engine:    "engine.example:443",
		Transport: &QUICTransport{},
	})
	if err == nil || !strings.Contains(err.Error(), "QUIC transport is not available") {
		t.Fatalf("NewClient() error = %v, want unavailable QUIC transport", err)
	}

	_, err = (&QUICTransport{}).Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err == nil || !strings.Contains(err.Error(), "QUIC transport is not available") {
		t.Fatalf("QUICTransport.Dial() error = %v, want unavailable QUIC transport", err)
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

func TestFIPSProfileAutoTransportUsesTLSOnly(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	var tlsCalls atomic.Int32
	var quicCalls atomic.Int32
	transport := &AutoTransport{
		tlsDialer: fipsTestDialer{dial: func(context.Context, string, *tls.Config) (net.Conn, error) {
			tlsCalls.Add(1)
			return client, nil
		}},
		quicDialer: fipsTestDialer{dial: func(context.Context, string, *tls.Config) (net.Conn, error) {
			quicCalls.Add(1)
			return nil, errors.New("QUIC must not be called")
		}},
	}
	conn, err := transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if conn != client {
		t.Fatalf("AutoTransport.Dial() connection = %T, want TLS test connection", conn)
	}
	if tlsCalls.Load() != 1 || quicCalls.Load() != 0 {
		t.Fatalf("transport calls: TLS=%d QUIC=%d", tlsCalls.Load(), quicCalls.Load())
	}
	if mode := transport.SelectedMode(); mode != TunnelTransportModeTLS {
		t.Fatalf("AutoTransport.SelectedMode() = %q, want %q", mode, TunnelTransportModeTLS)
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
		{name: "QUIC", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolQUIC)}, want: "published QUIC tunnels"},
		{name: "WebTTY", props: TunnelProperties{Protocol: ProtocolPtr(ProtocolWebTTY)}, want: "WebTTY tunnels"},
		{name: "HTTP3", props: TunnelProperties{HTTPVersion: HTTPVersionPtr(HTTP3)}, want: "published HTTP/3 tunnels"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateFIPSTunnelProperties(test.props)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateFIPSTunnelProperties() error = %v, want %q", err, test.want)
			}
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
