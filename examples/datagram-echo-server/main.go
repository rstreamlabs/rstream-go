// See LICENSE file in the project root for license information.

// datagram-echo-server runs a UDP datagram echo server behind an rstream
// DatagramTunnel. Every datagram received is sent back to the sender. When
// published as DTLS, any standard DTLS client can reach the server through the
// rstream edge without the server exposing a public UDP port.
//
// Run: go run . (internal only) or go run . -publish (published DTLS endpoint)

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func handleConnection(conn net.PacketConn) {
	defer conn.Close()
	buf := make([]byte, 2048)
	for {
		n, raddr, err := conn.ReadFrom(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Read error from %s: %v", raddr, err)
			}
			return
		}
		log.Printf("Received %d bytes from %s: %s\n", n, raddr, buf[:n])
		if _, err := conn.WriteTo(buf[:n], raddr); err != nil {
			log.Printf("Write error to %s: %v", raddr, err)
			return
		}
	}
}

func run(ctx context.Context, publish bool, minVersion string) error {
	client, err := config.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("load rstream configuration: %w", err)
	}
	defer client.Close()

	// Open control channel
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("datagram-echo"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolDTLS)
		if minVersion != "" {
			tunnelProps.TLSMinVersion = rstream.StringPtr(minVersion)
		}
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
	// Echo server
	for {
		conn, _, err := packetListener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("listener accept error: %w", err)
		}
		go handleConnection(conn)
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel")
	minVersion := flag.String("dtls-min-version", "", "minimum version tls1.2 or tls1.3; requires --publish")
	flag.Parse()
	switch *minVersion {
	case "tls1.2", "tls1.3":
		if !*publish {
			log.Fatal("--dtls-min-version requires --publish")
		}
	case "":
	default:
		log.Fatal("--dtls-min-version must be tls1.2 or tls1.3")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *publish, *minVersion); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
