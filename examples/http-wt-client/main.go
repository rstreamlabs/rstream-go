// See LICENSE file in the project root for license information.

// http-wt-client connects to http-wt-server over WebTransport through an
// rstream DatagramTunnel, opens a bidi stream, and sends a ping. rstream relays
// QUIC/HTTP3 datagrams end-to-end so browser-compatible WebTransport clients
// reach the upstream server without a dedicated QUIC endpoint.
//
// Run: go run . (rstream dialer) or go run . -publish (published WT endpoint)

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func handleConnection(ctx context.Context, sess *webtransport.Session) error {
	defer sess.CloseWithError(0, "")
	// Open a stream, send ping, read echo
	stream, err := sess.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("OpenStreamSync failed: %w", err)
	}
	defer stream.Close()
	if _, err := stream.Write([]byte("ping")); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}
	buf := make([]byte, 4)
	n, err := stream.Read(buf)
	if err != nil {
		return fmt.Errorf("read failed: %w", err)
	}
	fmt.Printf("Received: %s\n", string(buf[:n]))
	return nil
}

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	dialer := webtransport.Dialer{}
	name := "wt-example"
	var url *string
	if publish {
		// List tunnels to find the published host using rstream API (data plane)
		tunnels, err := client.ListTunnels(context.Background(), nil)
		if err != nil {
			log.Fatalf("failed to list tunnels: %v", err)
		}
		for _, tunnel := range *tunnels {
			if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
				host := *tunnel.Host
				if _, _, err := net.SplitHostPort(host); err != nil {
					if addrErr, ok := err.(*net.AddrError); ok && addrErr.Err == "missing port in address" {
						host = net.JoinHostPort(host, "443")
					} else {
						return fmt.Errorf("failed to split host and port: %w", err)
					}
				}
				url = rstream.StringPtr("https://" + host + "/webtransport")
				break
			}
		}
		if url == nil {
			log.Fatalf("tunnel %q not found or not published", name)
		}
	} else {
		// Dial the tunnel using rstream dialer (WebTransport)
		url = rstream.StringPtr("https://" + name + "/webtransport")
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
		dialer.DialAddr = func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			raddr := rstream.Addr{IdOrName: addr}
			conn, err := client.PacketDial(ctx, raddr)
			if err != nil {
				return nil, err
			}
			return quic.DialEarly(ctx, conn, &raddr, tlsCfg, cfg)
		}
		dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h3"},
		}
	}
	// Connect to the WebTransport server
	_, sess, err := dialer.Dial(ctx, *url, nil)
	if err != nil {
		return fmt.Errorf("webtransport connection failed: %w", err)
	}
	return handleConnection(ctx, sess)
}

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, client, *publish); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}
