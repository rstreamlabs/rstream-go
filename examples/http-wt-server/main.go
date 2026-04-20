// See LICENSE file in the project root for license information.

// http-wt-server starts a WebTransport server behind an rstream DatagramTunnel
// using quic-go/webtransport-go. It echoes data on every bidi stream. rstream
// exposes the server to WebTransport clients — including browsers — without
// requiring a public UDP port or edge QUIC termination infrastructure.
//
// Run: go run . (internal only) or go run . -publish (published WT endpoint)

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
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
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

func run(ctx context.Context, client *rstream.Client, publish bool, publishedProtocol string) error {
	// Open control channel
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("wt-example"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		switch strings.ToLower(strings.TrimSpace(publishedProtocol)) {
		case "http":
			tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
			tunnelProps.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP3)
		case "quic":
			tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolQUIC)
		default:
			return fmt.Errorf("invalid published protocol %q (expected http or quic)", publishedProtocol)
		}
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
	// Start a WebTransport server using the tunnel as a listener
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	mux := http.NewServeMux()
	server := webtransport.Server{
		H3: &http3.Server{
			Handler:         mux,
			TLSConfig:       tlsCfg,
			EnableDatagrams: true,
		},
	}
	webtransport.ConfigureHTTP3Server(server.H3)
	mux.HandleFunc("/webtransport", func(w http.ResponseWriter, r *http.Request) {
		sess, err := server.Upgrade(w, r)
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
	go func() { errCh <- server.Serve(rstream.PacketConnFromPacketListener(packetListener)) }()
	select {
	case <-ctx.Done():
		log.Println("Shutting down HTTP server...")
		return server.Close()
	case err := <-errCh:
		return fmt.Errorf("http server error: %w", err)
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel")
	publishedProtocol := flag.String("published-protocol", "quic", "published edge protocol to use when -publish=true (http or quic)")
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
	if err := run(ctx, client, *publish, *publishedProtocol); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
