// See LICENSE file in the project root for license information.

// http-sse-client connects to http-sse-server and streams Server-Sent Events
// through a private rstream HTTP tunnel or a published rstream HTTP endpoint.
//
// Run: go run . (rstream dialer) or go run . -publish (published HTTP endpoint)

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func findPublishedURL(ctx context.Context, client *rstream.Client, name string) (string, error) {
	tunnels, err := client.ListTunnels(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("failed to list tunnels: %w", err)
	}
	for _, tunnel := range *tunnels {
		if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
			return "https://" + *tunnel.Host + "/events", nil
		}
	}
	return "", fmt.Errorf("tunnel %q not found or not published", name)
}

func newHTTPClient(client *rstream.Client, publish bool) *http.Client {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	if publish {
		return httpClient
	}
	httpClient.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil || host == "" {
				return nil, fmt.Errorf("failed to extract host from address: %v", err)
			}
			return client.Dial(ctx, rstream.Addr{IdOrName: host})
		},
	}
	return httpClient
}

func streamEvents(ctx context.Context, httpClient *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected response status: %s", resp.Status)
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			fmt.Printf("SSE data: %s\n", data)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read SSE stream: %w", err)
	}
	return nil
}

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	name := "sse-example"
	url := "http://" + name + "/events"
	if *publish {
		url, err = findPublishedURL(ctx, client, name)
		if err != nil {
			log.Fatalf("Error: %v", err)
		}
	}
	if err := streamEvents(ctx, newHTTPClient(client, *publish), url); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
