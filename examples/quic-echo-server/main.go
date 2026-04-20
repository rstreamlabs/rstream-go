// See LICENSE file in the project root for license information.

// quic-echo-server starts a raw QUIC echo server behind an rstream
// DatagramTunnel. It echoes every stream message and datagram back to the
// sender. rstream enables custom QUIC protocols to be published at a managed
// edge endpoint without requiring the server to have a reachable UDP address.
//
// Run: go run . (internal only) or go run . -publish (published QUIC endpoint)

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
	"io"
	"log"
	"math/big"
	"os"
	"os/signal"
	"syscall"

	"github.com/quic-go/quic-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

// Published QUIC requires an explicit application ALPN; the engine mirrors it upstream
const quicEchoALPN = "rstream-quic-echo"

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
		NextProtos:   []string{quicEchoALPN},
	}, nil
}

func handleConnection(conn *quic.Conn) {
	defer conn.CloseWithError(0, "server done")
	go handleDatagrams(conn)
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			log.Printf("Failed to accept stream: %v", err)
			return
		}
		go handleStream(conn, stream)
	}
}

func handleStream(conn *quic.Conn, stream *quic.Stream) {
	defer stream.Close()
	buf := make([]byte, 2048)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			log.Printf("Received stream %d bytes from %s: %s\n", n, conn.RemoteAddr(), buf[:n])
			if _, err := stream.Write(buf[:n]); err != nil {
				log.Printf("Write error to %s: %v", conn.RemoteAddr(), err)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("Read error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}
	}
}

func handleDatagrams(conn *quic.Conn) {
	for {
		payload, err := conn.ReceiveDatagram(context.Background())
		if err != nil {
			log.Printf("Datagram read error from %s: %v", conn.RemoteAddr(), err)
			return
		}
		log.Printf("Received datagram %d bytes from %s: %s\n", len(payload), conn.RemoteAddr(), payload)
		if err := conn.SendDatagram(payload); err != nil {
			log.Printf("Write error to %s: %v", conn.RemoteAddr(), err)
			return
		}
	}
}

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	// Open control channel
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("quic-echo"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolQUIC)
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
	// Start a QUIC echo server using the tunnel as a listener
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	transport := quic.Transport{Conn: rstream.PacketConnFromPacketListener(packetListener)}
	listener, err := transport.Listen(tlsCfg, &quic.Config{EnableDatagrams: true})
	if err != nil {
		return fmt.Errorf("failed to start QUIC listener: %w", err)
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			return fmt.Errorf("listener accept error: %w", err)
		}
		go handleConnection(conn)
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel")
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
		log.Println("Got signal, exiting...")
		cancel()
	}()
	if err := run(ctx, client, *publish); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
