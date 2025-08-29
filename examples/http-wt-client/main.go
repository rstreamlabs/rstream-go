// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
	"github.com/rstreamlabs/rstream-go"
)

func main() {
	// 1. Create the WebTransport dialer (HTTP/3)
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	dialer := webtransport.Dialer{
		DialAddr: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			raddr := rstream.Addr{IdOrName: addr}
			conn, err := (&rstream.Client{}).PacketDial(ctx, raddr)
			if err != nil {
				return nil, err
			}
			return quic.DialEarly(ctx, conn, &raddr, tlsCfg, cfg)
		},
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h3"},
		},
	}
	// 2. Connect to the WebTransport server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, sess, err := dialer.Dial(ctx, "https://wt-example/webtransport", nil)
	if err != nil {
		log.Fatalf("WebTransport dial failed: %v", err)
	}
	defer sess.CloseWithError(0, "")
	// 3. Open a stream, send ping, read echo
	stream, err := sess.OpenStreamSync(ctx)
	if err != nil {
		log.Fatalf("OpenStreamSync failed: %v", err)
	}
	defer stream.Close()
	if _, err := stream.Write([]byte("ping")); err != nil {
		log.Fatalf("write failed: %v", err)
	}
	buf := make([]byte, 4)
	n, err := stream.Read(buf)
	if err != nil {
		log.Fatalf("read failed: %v", err)
	}
	fmt.Printf("Received: %s\n", string(buf[:n]))
}
