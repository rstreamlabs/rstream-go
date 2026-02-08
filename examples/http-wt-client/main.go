// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go"
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

func run(ctx context.Context, publish bool) error {
	dialer := webtransport.Dialer{}
	name := "wt-example"
	var url *string = nil
	if publish {
		// List tunnels to find the published host using rstream API (data plane)
		tunnels, err := (&rstream.Client{}).ListTunnels(context.Background(), nil)
		if err != nil {
			log.Fatalf("failed to list tunnels: %v", err)
		}
		for _, tunnel := range *tunnels {
			if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
				url = rstream.StringPtr("https://" + *tunnel.Host + "/webtransport")
				break
			}
		}
		if url == nil {
			log.Fatalf("tunnel %q not found or not published", name)
		}
	} else {
		url = rstream.StringPtr("https://" + name + "/webtransport")
		// Dial the tunnel using rstream dialer (HTTP/3)
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
		dialer.DialAddr = func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			raddr := rstream.Addr{IdOrName: addr}
			conn, err := (&rstream.Client{}).PacketDial(ctx, raddr)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, *publish); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}
