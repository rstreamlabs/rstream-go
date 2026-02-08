// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/rstreamlabs/rstream-go"
)

// NB : Any HTTP version can be used (HTTP/1.1, HTTP/2, HTTP/3) on client side for published tunnels.

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	flag.Parse()
	// Create the HTTP client
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	name := "h3-example"
	var url *string = nil
	if *publish {
		// List tunnels to find the published host using rstream API (data plane)
		tunnels, err := (&rstream.Client{}).ListTunnels(context.Background(), nil)
		if err != nil {
			log.Fatalf("failed to list tunnels: %v", err)
		}
		for _, tunnel := range *tunnels {
			if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
				url = rstream.StringPtr("https://" + *tunnel.Host + "/")
				break
			}
		}
		if url == nil {
			log.Fatalf("tunnel %q not found or not published", name)
		}
	} else {
		url = rstream.StringPtr("https://" + name + "/")
		// Dial the tunnel using rstream dialer (HTTP/3)
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
		httpClient.Transport = &http3.Transport{
			Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host == "" {
					return nil, fmt.Errorf("failed to extract host from address: %v", err)
				}
				raddr := rstream.Addr{
					IdOrName: host,
				}
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
	}
	// Make the HTTP request
	resp, err := httpClient.Get(*url)
	if err != nil {
		log.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()
	// Read and print the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Failed to read response body: %v", err)
	}
	fmt.Printf("Response status: %s\n", resp.Status)
	fmt.Printf("Response body:\n%s", body)
}
