// See LICENSE file in the project root for license information.

// http-matrix-server is the server side of the end-to-end HTTP coverage matrix.
// It creates a tunnel for the requested upstream HTTP version and serves a
// /ping → "pong\n" handler so the test client can verify the full relay path.
//
// Upstream variants: h1 (BytestreamTunnel, HTTP/1.1), h2c (BytestreamTunnel,
// HTTP/2 cleartext), h3 (DatagramTunnel, HTTP/3 via quic-go).

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/quic-go/quic-go/http3"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{tlsCert}, NextProtos: []string{"h3"}}, nil
}

func handler(w http.ResponseWriter, _ *http.Request) {
	fmt.Fprint(w, "pong\n")
}

func runH1(ctx context.Context, tunnel rstream.BytestreamTunnel) error {
	srv := &http.Server{Handler: http.HandlerFunc(handler)}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err := srv.Serve(tunnel)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runH2C(ctx context.Context, tunnel rstream.BytestreamTunnel) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", handler)
	srv := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err := srv.Serve(tunnel)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runH3(ctx context.Context, tunnel rstream.DatagramTunnel) error {
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("TLS config: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", handler)
	srv := &http3.Server{
		TLSConfig: tlsCfg,
		Handler:   mux,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(rstream.PacketConnFromPacketListener(tunnel))
	}()
	select {
	case <-ctx.Done():
		_ = srv.Close()
		return nil
	case err := <-errCh:
		return err
	}
}

func run(ctx context.Context, client *rstream.Client, upstream, name string) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer ctrl.Close()
	props := rstream.TunnelProperties{
		Name: rstream.StringPtr(name),
	}
	switch upstream {
	case "h2c":
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP2)
	case "h3":
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeDatagram)
		props.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP3)
	default:
		props.Type = rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)
	}
	tunnel, err := ctrl.CreateTunnel(ctx, props)
	if err != nil {
		return fmt.Errorf("create tunnel: %w", err)
	}
	defer tunnel.Close()
	fwdAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("forwarding address: %w", err)
	}
	fmt.Printf("READY %s\n", fwdAddr)
	switch upstream {
	case "h2c":
		bs, ok := tunnel.(rstream.BytestreamTunnel)
		if !ok {
			return fmt.Errorf("tunnel is not BytestreamTunnel")
		}
		return runH2C(ctx, bs)
	case "h3":
		dg, ok := tunnel.(rstream.DatagramTunnel)
		if !ok {
			return fmt.Errorf("tunnel is not DatagramTunnel")
		}
		return runH3(ctx, dg)
	default:
		bs, ok := tunnel.(rstream.BytestreamTunnel)
		if !ok {
			return fmt.Errorf("tunnel is not BytestreamTunnel")
		}
		return runH1(ctx, bs)
	}
}

func main() {
	upstream := flag.String("upstream", "h1", "upstream protocol: h1, h2c, h3")
	name := flag.String("name", "", "tunnel name (default: http-matrix-<upstream>)")
	flag.Parse()
	if *name == "" {
		*name = "http-matrix-" + *upstream
	}
	if *upstream == "h3" {
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	}
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	if err := run(ctx, client, *upstream, *name); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
