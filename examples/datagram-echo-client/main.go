// See LICENSE file in the project root for license information.

// datagram-echo-client connects to the datagram-echo-server through an rstream
// DatagramTunnel, sends a message, and prints the echoed reply. rstream relays
// UDP datagrams through a secure DTLS tunnel, so neither endpoint needs a
// public IP or open firewall port.
//
// Run: go run . (rstream dialer) or go run . -publish (published DTLS endpoint)

package main

import (
	"context"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/protocol"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func handleConnection(packetConn net.PacketConn, raddr net.Addr) error {
	defer packetConn.Close()
	if err := packetConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set echo deadline: %w", err)
	}
	// Send a message
	msg := []byte("Hello from rstream-go!")
	n, err := packetConn.WriteTo(msg, raddr)
	if err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	log.Printf("Wrote %d bytes: %s", n, msg)
	// Receive a message
	buf := make([]byte, 2048)
	n, addr, err := packetConn.ReadFrom(buf)
	if err != nil {
		return fmt.Errorf("failed to read: %w", err)
	}
	log.Printf("Received %d bytes from %s: %s", n, addr, buf[:n])
	return nil
}

func publishedOptions(version, caFile string) ([]dtls.ClientOption, error) {
	var options []dtls.ClientOption
	if version != "" {
		var v protocol.Version
		switch version {
		case "1.2":
			v = protocol.Version1_2
		case "1.3":
			v = protocol.Version1_3
		default:
			return nil, fmt.Errorf("unsupported DTLS version %q (use 1.2 or 1.3)", version)
		}
		options = append(options, dtls.WithMinVersion(v), dtls.WithMaxVersion(v))
	}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read downstream CA: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("no certificates in downstream CA file %q", caFile)
		}
		options = append(options, dtls.WithRootCAs(roots))
	}
	return options, nil
}

func run(ctx context.Context, publish bool, options []dtls.ClientOption) error {
	client, err := config.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("load rstream configuration: %w", err)
	}
	defer client.Close()

	const name = "datagram-echo"
	if !publish {
		raddr := rstream.Addr{IdOrName: name}
		packetConn, err := client.PacketDial(ctx, raddr)
		if err != nil {
			return fmt.Errorf("failed to dial tunnel: %w", err)
		}
		return handleConnection(packetConn, &raddr)
	}

	tunnels, err := client.ListTunnels(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list tunnels: %w", err)
	}
	for _, tunnel := range *tunnels {
		if tunnel.Name == nil || *tunnel.Name != name || tunnel.Host == nil {
			continue
		}
		hostname, port, err := net.SplitHostPort(*tunnel.Host)
		if addrErr, ok := err.(*net.AddrError); ok && addrErr.Err == "missing port in address" {
			hostname, port = *tunnel.Host, "4433"
		} else if err != nil {
			return fmt.Errorf("failed to split published address: %w", err)
		}
		raddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(hostname, port))
		if err != nil {
			return fmt.Errorf("failed to resolve published address: %w", err)
		}
		conn, err := dtls.Dial("udp", raddr, append(options, dtls.WithServerName(hostname))...)
		if err != nil {
			return fmt.Errorf("failed to dial published host: %w", err)
		}
		handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := conn.HandshakeContext(handshakeCtx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("published DTLS handshake: %w", err)
		}
		return handleConnection(rstream.PacketConnFromConn(conn, nil, rstream.PacketModeRaw), raddr)
	}
	return fmt.Errorf("tunnel %q not found or not published", name)
}

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	version := flag.String("dtls-version", "", "offer only DTLS 1.2 or 1.3; requires --publish")
	caFile := flag.String("ca-file", "", "verify the published hostname with this PEM CA bundle; requires --publish")
	flag.Parse()
	if !*publish && (*version != "" || *caFile != "") {
		log.Fatal("--dtls-version and --ca-file require --publish")
	}
	options, err := publishedOptions(*version, *caFile)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *publish, options); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
