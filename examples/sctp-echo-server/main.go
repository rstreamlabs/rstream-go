// See LICENSE file in the project root for license information.

// sctp-echo-server runs a pion/sctp echo server behind an rstream
// DatagramTunnel. In private mode SCTP runs over the SDK datagram tunnel. In
// published mode clients connect through the engine DTLS listener and SCTP is
// carried as the DTLS application protocol.
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

	"github.com/pion/sctp"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func handleStream(stream *sctp.Stream) {
	defer stream.Close()
	buf := make([]byte, 2048)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if _, werr := stream.WriteSCTP(buf[:n], sctp.PayloadTypeWebRTCString); werr != nil {
				log.Printf("SCTP write error: %v", werr)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("SCTP read error: %v", err)
			}
			return
		}
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	assoc, err := sctp.Server(sctp.Config{NetConn: conn})
	if err != nil {
		log.Printf("SCTP association error: %v", err)
		return
	}
	defer assoc.Close()
	for {
		stream, err := assoc.AcceptStream()
		if err != nil {
			return
		}
		go handleStream(stream)
	}
}

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("sctp-echo"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolDTLS)
	}
	tunnel, err := ctrl.CreateTunnel(ctx, tunnelProps)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer tunnel.Close()
	go func() {
		<-ctx.Done()
		tunnel.Close()
	}()
	forwardingAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("failed to get forwarding address: %w", err)
	}
	packetListener, ok := tunnel.(rstream.PacketListener)
	if !ok {
		return fmt.Errorf("tunnel does not implement rstream.PacketListener")
	}
	fmt.Printf("SCTP server listening on %s\n", forwardingAddr)
	for {
		packetConn, raddr, err := packetListener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("listener accept error: %w", err)
		}
		go handleConnection(rstream.ConnFromPacketConn(packetConn, raddr))
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel on the engine DTLS listener")
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
