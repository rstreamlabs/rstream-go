// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/rstreamlabs/rstream-go"
)

func main() {
	// 1. Create the HTTP client (HTTP/3)
	httpClient := &http.Client{
		Transport: &http3.Transport{
			Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (quic.EarlyConnection, error) {
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
		},
		Timeout: 5 * time.Second,
	}
	// 2. Make the HTTP request
	resp, err := httpClient.Get("https://h3-example/")
	if err != nil {
		log.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()
	// 3. Read and print the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Failed to read response body: %v", err)
	}
	fmt.Printf("Response status: %s\n", resp.Status)
	fmt.Printf("Response body:\n%s", body)
}
