// See LICENSE file in the project root for license information.

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

func run(ctx context.Context, publish bool) error {
	name := "stream-echo"
	if publish {
		// List tunnels to find the published host using rstream control API
		tunnels, err := (&rstream.Client{}).ListTunnels(ctx, nil)
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
		// Dial the tunnel using custom rstream dialer
		conn, err := (&rstream.Client{}).Dial(ctx, rstream.Addr{IdOrName: name})
		if err != nil {
			return fmt.Errorf("failed to dial tunnel: %w", err)
		}
		return handleConnection(conn)
	}
}

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Got signal, exiting...")
		cancel()
	}()
	if err := run(ctx, *publish); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
