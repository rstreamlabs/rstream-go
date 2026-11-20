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

func run(publish bool) error {
	client, err := config.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("load rstream configuration: %w", err)
	}
	defer client.Close()
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}
	defer httpClient.CloseIdleConnections()
	const name = "h1-example"
	var url string
	if publish {
		// List tunnels to find the published host using the Engine API.
		tunnels, err := client.ListTunnels(context.Background(), nil)
		if err != nil {
			return fmt.Errorf("list tunnels: %w", err)
		}
		for _, tunnel := range *tunnels {
			if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
				url = "https://" + *tunnel.Host + "/"
				break
			}
		}
		if url == "" {
			return fmt.Errorf("tunnel %q not found or not published", name)
		}
	} else {
		url = "http://" + name + "/"
		// Dial the tunnel using rstream dialer (HTTP/1.1)
		httpClient.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("extract host from HTTP address: %w", err)
				}
				return client.Dial(ctx, rstream.Addr{IdOrName: host})
			},
		}
	}
	// Make the HTTP request
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("make HTTP request: %w", err)
	}
	defer resp.Body.Close()
	// Read and print the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read HTTP response: %w", err)
	}
	fmt.Printf("Response status: %s\n", resp.Status)
	fmt.Printf("Response body:\n%s", body)
	return nil
}

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	flag.Parse()
	if err := run(*publish); err != nil {
		log.Fatal(err)
	}
}
