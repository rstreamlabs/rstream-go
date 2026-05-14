// See LICENSE file in the project root for license information.

// sctp-echo-client connects to sctp-echo-server, opens an SCTP stream with
// pion/sctp, sends one message, and prints the echoed reply.
//
// Run: go run . (rstream dialer) or go run . -publish (published DTLS endpoint)

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func hostPortFromAddr(addr, defaultPort string) string {
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		addr = addr[:i]
	}
	if i := strings.IndexByte(addr, ' '); i >= 0 {
		addr = addr[:i]
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, defaultPort)
	}
	return addr
}

func findPublishedHost(ctx context.Context, client *rstream.Client, name string) (string, error) {
	tunnels, err := client.ListTunnels(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to list tunnels: %w", err)
	}
	for _, tunnel := range *tunnels {
		if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
			return *tunnel.Host, nil
		}
	}
	return "", fmt.Errorf("tunnel %q not found or not published", name)
}

func sctpEcho(conn net.Conn) error {
	defer conn.Close()
	assoc, err := sctp.Client(sctp.Config{NetConn: conn})
	if err != nil {
		return fmt.Errorf("failed to create SCTP association: %w", err)
	}
	defer assoc.Close()
	stream, err := assoc.OpenStream(0, sctp.PayloadTypeWebRTCString)
	if err != nil {
		return fmt.Errorf("failed to open SCTP stream: %w", err)
	}
	defer stream.Close()
	msg := []byte("Hello from rstream-go over SCTP!")
	if err := stream.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("failed to set SCTP stream deadline: %w", err)
	}
	if _, err := stream.Write(msg); err != nil {
		return fmt.Errorf("failed to write SCTP message: %w", err)
	}
	buf := make([]byte, 2048)
	n, err := stream.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read SCTP reply: %w", err)
	}
	fmt.Printf("SCTP echo: %s\n", buf[:n])
	return nil
}

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	name := "sctp-echo"
	if publish {
		host, err := findPublishedHost(ctx, client, name)
		if err != nil {
			return err
		}
		hp := hostPortFromAddr(host, "4433")
		hostname, _, _ := net.SplitHostPort(hp)
		udpAddr, err := net.ResolveUDPAddr("udp", hp)
		if err != nil {
			return fmt.Errorf("failed to resolve published host: %w", err)
		}
		dtlsConn, err := dtls.Dial("udp", udpAddr, &dtls.Config{
			ServerName: hostname,
		})
		if err != nil {
			return fmt.Errorf("failed to dial published DTLS host: %w", err)
		}
		return sctpEcho(dtlsConn)
	}
	raddr := rstream.Addr{IdOrName: name}
	packetConn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("failed to dial tunnel: %w", err)
	}
	return sctpEcho(rstream.ConnFromPacketConn(packetConn, &raddr))
}

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := run(ctx, client, *publish); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
