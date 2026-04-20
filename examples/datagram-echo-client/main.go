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
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/pion/dtls/v3"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func handleConnection(packetConn net.PacketConn, raddr net.Addr) error {
	defer packetConn.Close()
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

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	name := "datagram-echo"
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
				port := "4433"
				if parsedHost, parsedPort, err := net.SplitHostPort(host); err == nil {
					hostname = parsedHost
					port = parsedPort
				} else if addrErr, ok := err.(*net.AddrError); !ok || addrErr.Err != "missing port in address" {
					return fmt.Errorf("failed to split host and port: %w", err)
				}
				// Connect to the published host using standard DTLS dialer
				raddr := &net.UDPAddr{IP: func() net.IP {
					ips, err := net.LookupIP(hostname)
					if err != nil || len(ips) == 0 {
						return net.IPv4zero
					}
					return ips[0]
				}(),
					Port: func() int {
						p, err := net.LookupPort("udp", port)
						if err != nil {
							return 0
						}
						return p
					}()}
				packetConn, err := dtls.Dial("udp", raddr, &dtls.Config{ServerName: hostname})
				if err != nil {
					return fmt.Errorf("failed to dial published host: %w", err)
				}
				return handleConnection(rstream.PacketConnFromDTLSConn(packetConn), raddr)
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
		return handleConnection(packetConn, &raddr)
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
