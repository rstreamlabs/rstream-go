// See LICENSE file in the project root for license information.

//go:build !rstream_fips

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/protocol"
	rstream "github.com/rstreamlabs/rstream-go"
)

func TestPublishedDTLSVersionOptionsAndRawAdapter(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"echo.example.test"}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	for _, tc := range []struct {
		value string
		want  protocol.Version
	}{
		{"1.2", protocol.Version1_2}, {"1.3", protocol.Version1_3}, {"", protocol.Version1_3},
	} {
		t.Run(tc.value, func(t *testing.T) {
			listener, err := dtls.ListenAddr("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}, dtls.WithCertificates(certificate), dtls.WithSupportedProtocols("echo"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(time.Second))
				packet := make([]byte, 64)
				n, err := connection.Read(packet)
				if err == nil {
					_, err = connection.Write(packet[:n])
				}
				done <- err
			}()
			options, err := dtlsClientOptions("echo.example.test", "echo", tc.value, "", 160)
			if err != nil {
				t.Fatal(err)
			}
			client, err := dtls.Dial("udp", listener.Addr().(*net.UDPAddr), options...)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if err := client.HandshakeContext(ctx); err != nil {
				t.Fatal(err)
			}
			state, ok := client.ConnectionState()
			if !ok || state.NegotiatedVersion() != tc.want || state.NegotiatedProtocol != "echo" {
				t.Fatalf("unexpected DTLS negotiation: %+v", state)
			}
			adapter := rstream.PacketConnFromConn(client, nil, rstream.PacketModeRaw)
			_ = adapter.SetDeadline(time.Now().Add(time.Second))
			if _, err := adapter.WriteTo([]byte("one-datagram"), client.RemoteAddr()); err != nil {
				t.Fatal(err)
			}
			packet := make([]byte, 64)
			n, _, err := adapter.ReadFrom(packet)
			if err != nil || string(packet[:n]) != "one-datagram" {
				t.Fatalf("adapter echo=%q error=%v", packet[:n], err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPublishedDTLSOptionsRejectInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		version string
		mtu     int
	}{
		{"1.1", 160}, {"1.4", 160}, {"TLS1.3", 160}, {"", -1}, {"", 32}, {"", 65536},
	} {
		if _, err := dtlsClientOptions("echo.example.test", "echo", tc.version, "", tc.mtu); err == nil {
			t.Fatalf("invalid DTLS options accepted: %+v", tc)
		}
	}
}
