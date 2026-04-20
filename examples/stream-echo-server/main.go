// See LICENSE file in the project root for license information.

// stream-echo-server starts a raw byte-stream echo server behind an rstream
// BytestreamTunnel. Every byte received is written back to the sender. This is
// the minimal rstream example: any bidirectional TCP-like protocol is tunneled
// by wrapping a net.Listener with no further SDK involvement.
//
// Run: go run . (internal only) or go run . -publish (published TLS endpoint)

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

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	// Open control channel
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("stream-echo"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolTLS)
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
	listener, ok := tunnel.(net.Listener)
	if !ok {
		return fmt.Errorf("tunnel does not implement net.Listener")
	}
	fmt.Printf("Server listening on %s\n", forwardingAddr)
	// Echo server
	for {
		conn, err := listener.Accept()
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
