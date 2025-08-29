// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
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
	return &tls.Config{Certificates: []tls.Certificate{tlsCert}, NextProtos: []string{"h3"}}, nil
}

func run(ctx context.Context) error {
	// 1. Open control channel
	ctrl, err := (&rstream.Client{}).Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// 2. Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:        rstream.StringPtr("wt-example"),
		Type:        rstream.TunnelTypePtr(rstream.TunnelDatagram),
		Publish:     rstream.BoolPtr(true),
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP3),
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
	packetListener, ok := tunnel.(rstream.PacketListener)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.PacketListener")
	}
	fmt.Printf("Server listening on %s\n", forwardingAddr)
	// 3. Start a WebTransport server using the tunnel as a packet listener (HTTP/3)
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	mux := http.NewServeMux()
	srv := webtransport.Server{H3: http3.Server{Handler: mux, TLSConfig: tlsCfg}}
	mux.HandleFunc("/webtransport", func(w http.ResponseWriter, r *http.Request) {
		sess, err := srv.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		go func() {
			for {
				stream, err := sess.AcceptStream(r.Context())
				if err != nil {
					return
				}
				go func() {
					defer stream.Close()
					buf := make([]byte, 4096)
					for {
						n, err := stream.Read(buf)
						if err != nil {
							return
						}
						if _, err := stream.Write(buf[:n]); err != nil {
							return
						}
					}
				}()
			}
		}()
	})
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(rstream.PacketConnFromPacketListener(packetListener)) }()
	select {
	case <-ctx.Done():
		log.Println("Shutting down HTTP server...")
		return srv.Close()
	case err := <-errCh:
		return fmt.Errorf("http server error: %w", err)
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, exiting...")
		cancel()
	}()
	if err := run(ctx); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
