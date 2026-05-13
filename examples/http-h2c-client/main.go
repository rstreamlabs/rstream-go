// See LICENSE file in the project root for license information.

// http-h2c-client makes an HTTP/2 cleartext (h2c) GET request to
// http-h2c-server through an rstream BytestreamTunnel. TLS is enforced at the
// rstream edge while the upstream leg stays unencrypted h2c, giving multiplexed
// HTTP/2 without double-encrypting the internal path.
//
// Run: go run . (rstream dialer) or go run . -publish (published HTTP/2 endpoint)

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
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"golang.org/x/net/http2"
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
		Timeout:       5 * time.Second,
		CheckRedirect: rejectCrossHostRedirects,
	}
	name := "h2c-example"
	var url *string = nil
	if *publish {
		// List tunnels to find the published host using the Engine API.
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
		// Dial the tunnel using rstream dialer (HTTP/2, h2c)
		httpClient.Transport = &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || host == "" {
					return nil, fmt.Errorf("failed to extract host from address: %v", err)
				}
				if !strings.EqualFold(host, name) {
					return nil, fmt.Errorf("redirect to unexpected tunnel host %q", host)
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

func rejectCrossHostRedirects(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
		return http.ErrUseLastResponse
	}
	return nil
}
