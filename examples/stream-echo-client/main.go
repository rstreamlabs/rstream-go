// See LICENSE file in the project root for license information.

// stream-echo-client connects to stream-echo-server through an rstream
// BytestreamTunnel, writes a message over the raw byte stream, and prints the
// echo. This is the simplest possible rstream tunnel demo: any TCP-like
// protocol works through a BytestreamTunnel with zero protocol changes.
//
// Run: go run . (rstream dialer) or go run . -publish (published TLS endpoint)

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func handleConnection(conn net.Conn) error {
	defer conn.Close()
	// Send a message
	msg := []byte("Hello from rstream-go!")
	n, err := conn.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	log.Printf("Wrote %d bytes: %s", n, msg)
	// Receive a message
	buf := make([]byte, 2048)
	n, err = conn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read: %w", err)
	}
	log.Printf("Received %d bytes: %s", n, buf[:n])
	return nil
}

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	name := "stream-echo"
	if publish {
		// List tunnels to find the published host using rstream API (data plane)
		tunnels, err := client.ListTunnels(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to list tunnels: %w", err)
		}
		for _, tunnel := range *tunnels {
			if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
				host := *tunnel.Host
				hostname, _, err := net.SplitHostPort(*tunnel.Host)
				if err != nil {
					return fmt.Errorf("failed to split host and port: %w", err)
				}
				// Connect to the published host using standard TLS dialer
				conn, err := tls.Dial("tcp", host, &tls.Config{ServerName: hostname})
				if err != nil {
					return fmt.Errorf("failed to dial published host: %w", err)
				}
				return handleConnection(conn)
			}
		}
		return fmt.Errorf("tunnel %q not found or not published", name)
	} else {
		// Dial the tunnel using rstream dialer
		conn, err := client.Dial(ctx, rstream.Addr{IdOrName: name})
		if err != nil {
			return fmt.Errorf("failed to dial tunnel: %w", err)
		}
		return handleConnection(conn)
	}
}

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
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
