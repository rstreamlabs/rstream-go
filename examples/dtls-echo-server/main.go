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
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/pion/dtls/v3"
	"github.com/rstreamlabs/rstream-go"
)

func generateDTLSConfig() (*dtls.Config, error) {
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
	return &dtls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}, nil
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 2048)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Read error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}
		log.Printf("Received %d bytes from %s: %s\n", n, conn.RemoteAddr(), buf[:n])
		if _, err := conn.Write(buf[:n]); err != nil {
			log.Printf("Write error to %s: %v", conn.RemoteAddr(), err)
			return
		}
	}
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
		Name:    rstream.StringPtr("dtls-echo"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelDatagram),
		Publish: rstream.BoolPtr(true),
	}
	tunnel, err := ctrl.CreateTunnel(ctx, tunnelProps)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	forwardingAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("failed to get forwarding address: %w", err)
	}
	go func() {
		<-ctx.Done()
		tunnel.Close()
	}()
	packetListener, ok := tunnel.(rstream.PacketListener)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.PacketListener")
	}
	fmt.Printf("Server listening on %s\n", forwardingAddr)
	// 3. Echo server
	dtlsCfg, err := generateDTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to generate TLS config: %w", err)
	}
	listener, err := dtls.NewListener(packetListener, dtlsCfg)
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("listener accept error: %w", err)
		}
		go handleConnection(conn)
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Got signal, exiting...")
		cancel()
	}()
	if err := run(ctx); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
