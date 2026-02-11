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

	"github.com/quic-go/quic-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func handleConnection(conn *quic.Conn) error {
	defer conn.CloseWithError(0, "client done")
	// Open a single stream
	stream, err := conn.OpenStream()
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	// Send a message
	msg := []byte("Hello from rstream-go!")
	n, err := stream.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	log.Printf("Wrote %d bytes: %s", n, msg)
	// Receive a message
	buf := make([]byte, 2048)
	n, err = stream.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read: %w", err)
	}
	log.Printf("Received %d bytes: %s", n, buf[:n])
	return nil
}

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	name := "quic-echo"
	if publish {
		// List tunnels to find the published host using rstream API (data plane)
		tunnels, err := client.ListTunnels(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to list tunnels: %w", err)
		}
		for _, tunnel := range *tunnels {
			if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
				host := *tunnel.Host
				hostname, _, err := net.SplitHostPort(host)
				if err != nil {
					return fmt.Errorf("failed to split host and port: %w", err)
				}
				conn, err := quic.DialAddr(ctx, host, &tls.Config{ServerName: hostname}, nil)
				if err != nil {
					return fmt.Errorf("failed to dial published host: %w", err)
				}
				return handleConnection(conn)
			}
		}
		return fmt.Errorf("tunnel %q not found or not published", name)
	} else {
		// Dial the tunnel using rstream dialer
		raddr := rstream.Addr{IdOrName: name}
		packetConn, err := client.PacketDial(ctx, raddr)
		if err != nil {
			return fmt.Errorf("failed to dial tunnel: %w", err)
		}
		tlsCfg := &tls.Config{
			InsecureSkipVerify: true,
		}
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
		transport := quic.Transport{
			Conn: packetConn,
		}
		conn, err := transport.Dial(ctx, &raddr, tlsCfg, nil)
		if err != nil {
			return fmt.Errorf("failed to dial QUIC server: %w", err)
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
