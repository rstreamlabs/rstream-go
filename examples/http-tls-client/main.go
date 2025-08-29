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

	"github.com/rstreamlabs/rstream-go"
)

func main() {
	// 1. Create the HTTP client (HTTP/1.1, HTTP/2, over TLS)
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host == "" {
					return nil, fmt.Errorf("failed to extract host from address: %v", err)
				}
				return (&rstream.Client{}).Dial(ctx, rstream.Addr{IdOrName: host})
			},
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				NextProtos:         []string{"h2", "http/1.1"},
			},
			ForceAttemptHTTP2: true,
		},
		Timeout: 5 * time.Second,
	}
	// 2. Make the HTTP request
	resp, err := httpClient.Get("https://http-tls-example/")
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
