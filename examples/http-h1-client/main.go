// See LICENSE file in the project root for license information.

// http-h1-client makes an HTTP/1.1 GET request to http-h1-server through an
// rstream BytestreamTunnel and prints the response. rstream gives any HTTP/1.1
// service a secure, publicly reachable endpoint with no infrastructure changes.
//
// Run: go run . (rstream dialer) or go run . -publish (published HTTP endpoint)

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

// NB : Any HTTP version can be used (HTTP/1.1, HTTP/2, HTTP/3) on client side for published tunnels.

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	// Create the HTTP client
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	name := "h1-example"
	var url *string = nil
	if *publish {
		// List tunnels to find the published host using rstream API (data plane)
		tunnels, err := client.ListTunnels(context.Background(), nil)
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
		url = rstream.StringPtr("http://" + name + "/")
		// Dial the tunnel using rstream dialer (HTTP/1.1)
		httpClient.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host == "" {
					return nil, fmt.Errorf("failed to extract host from address: %v", err)
				}
				return client.Dial(ctx, rstream.Addr{IdOrName: host})
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
