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
	"time"

	"github.com/quic-go/quic-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

// Published QUIC requires an explicit application ALPN; the engine mirrors it upstream
const quicEchoALPN = "rstream-quic-echo"

func handleConnection(ctx context.Context, conn *quic.Conn) error {
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
	log.Printf("Received stream %d bytes: %s", n, buf[:n])

	dgram := []byte("Datagram from rstream-go!")
	if err := conn.SendDatagram(dgram); err != nil {
		return fmt.Errorf("failed to send datagram: %w", err)
	}
	log.Printf("Sent datagram %d bytes: %s", len(dgram), dgram)
	dgramCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	echo, err := conn.ReceiveDatagram(dgramCtx)
	if err != nil {
		return fmt.Errorf("failed to receive datagram: %w", err)
	}
	log.Printf("Received datagram %d bytes: %s", len(echo), echo)
	return nil
}

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	name := "quic-echo"
	quicCfg := &quic.Config{EnableDatagrams: true}
	if publish {
		// List tunnels to find the published host using rstream API (data plane)
		tunnels, err := client.ListTunnels(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to list tunnels: %w", err)
		}
		for _, tunnel := range *tunnels {
			if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
				host := *tunnel.Host
				hostname := host
				if parsedHost, _, err := net.SplitHostPort(host); err == nil {
					hostname = parsedHost
				} else if addrErr, ok := err.(*net.AddrError); ok && addrErr.Err == "missing port in address" {
					host = net.JoinHostPort(host, "443")
				} else {
					return fmt.Errorf("failed to split host and port: %w", err)
				}
				conn, err := quic.DialAddr(ctx, host, &tls.Config{ServerName: hostname, NextProtos: []string{quicEchoALPN}}, quicCfg)
				if err != nil {
					return fmt.Errorf("failed to dial published host: %w", err)
				}
				return handleConnection(ctx, conn)
			}
		}
		return fmt.Errorf("tunnel %q not found or not published", name)
	}
	// Dial the tunnel using rstream dialer
	raddr := rstream.Addr{IdOrName: name}
	packetConn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("failed to dial tunnel: %w", err)
	}
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{quicEchoALPN},
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	transport := quic.Transport{Conn: packetConn}
	conn, err := transport.Dial(ctx, &raddr, tlsCfg, quicCfg)
	if err != nil {
		return fmt.Errorf("failed to dial QUIC server: %w", err)
	}
	return handleConnection(ctx, conn)
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
