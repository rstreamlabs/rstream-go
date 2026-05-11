// See LICENSE file in the project root for license information.

// http-tls-server starts an HTTPS server that terminates TLS itself behind an
// rstream BytestreamTunnel in pass-through mode. The rstream edge forwards raw
// bytes without decrypting them, so the server retains full control over its
// certificate and TLS configuration.
//
// Run: go run . (internal only) or go run . -publish (published TLS pass-through)

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"http-tls-example", "localhost"},
	}
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
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func handler(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	w.Header().Set("Server", "rstream-go-example/1.0")
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, hostname)
}

func run(ctx context.Context, client *rstream.Client, publish bool, certFile, keyFile string) error {
	// Open control channel
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("http-tls-example"),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
		tunnelProps.HTTPUseTLS = rstream.BoolPtr(true)
	}
	tunnel, err := ctrl.CreateTunnel(ctx, tunnelProps)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer tunnel.Close()
	forwardingAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("failed to get forwarding address: %w", err)
	}
	netListener, ok := tunnel.(interface{ net.Listener })
	if !ok {
		return fmt.Errorf("tunnel does not implement net.Listener")
	}
	fmt.Printf("Server listening on %s\n", forwardingAddr)
	// Start an HTTP server using the tunnel as a listener (HTTP/1.1, HTTP/2, over TLS)
	tlsCfg, err := loadTLSConfig(publish, certFile, keyFile)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(handler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(tls.NewListener(netListener, tlsCfg))
	}()
	select {
	case <-ctx.Done():
		log.Println("Shutting down HTTP server...")
		return server.Shutdown(context.Background())
	case err := <-errCh:
		return fmt.Errorf("http server error: %w", err)
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel")
	certFile := flag.String("cert-file", "", "TLS certificate file for published mode")
	keyFile := flag.String("key-file", "", "TLS private key file for published mode")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, exiting...")
		cancel()
	}()
	if err := run(ctx, client, *publish, *certFile, *keyFile); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func loadTLSConfig(publish bool, certFile, keyFile string) (*tls.Config, error) {
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("--cert-file and --key-file must be provided together")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
			MinVersion:   tls.VersionTLS12,
		}, nil
	}
	if publish {
		return nil, fmt.Errorf("published TLS mode requires --cert-file and --key-file for a certificate valid for the published host")
	}
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to generate TLS config: %w", err)
	}
	return tlsCfg, nil
}
