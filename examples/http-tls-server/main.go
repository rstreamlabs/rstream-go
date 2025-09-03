// See LICENSE file in the project root for license information.

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

	"github.com/rstreamlabs/rstream-go"
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
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"h2", "http/1.1"},
	}, nil
}

func handler(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	w.Header().Set("Server", "rstream-go-example/1.0")
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, hostname)
}

func run(ctx context.Context, publish bool) error {
	// Open control channel
	ctrl, err := (&rstream.Client{}).Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:       rstream.StringPtr("http-tls-example"),
		Publish:    rstream.BoolPtr(publish),
		Protocol:   rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPUseTLS: rstream.BoolPtr(true),
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
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}
	server := &http.Server{
		Handler: http.HandlerFunc(handler),
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
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, exiting...")
		cancel()
	}()
	if err := run(ctx, *publish); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
