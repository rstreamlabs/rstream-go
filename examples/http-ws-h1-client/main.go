// See LICENSE file in the project root for license information.

// http-ws-h1-client connects to an rstream WebSocket tunnel using gorilla/websocket,
// sends a "ping" message, and prints the reply. The client code is identical
// regardless of the upstream HTTP version (H1, H2C, or H3) — rstream handles
// protocol translation transparently. Pair with http-ws-server (H1),
// http-ws-h2c-server (H2C), or http-ws-h3-server (H3).
//
// Run: go run . (rstream dialer, default tunnel ws-example)
//
//	go run . -tunnel ws-h2c-example   (connect to http-ws-h2c-server)
//	go run . -tunnel ws-h3-example    (connect to http-ws-h3-server)

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func handleConnection(conn *websocket.Conn) error {
	defer conn.Close()
	// Send a ping and read the response
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read message: %w", err)
	}
	fmt.Printf("Received: %s\n", string(msg))
	return nil
}

func run(ctx context.Context, client *rstream.Client, publish bool, name string) error {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	var url *string
	if publish {
		// List tunnels to find the published host using the Engine API.
		tunnels, err := client.ListTunnels(context.Background(), nil)
		if err != nil {
			log.Fatalf("failed to list tunnels: %v", err)
		}
		for _, tunnel := range *tunnels {
			if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
				url = rstream.StringPtr("wss://" + *tunnel.Host + "/websocket")
				break
			}
		}
		if url == nil {
			log.Fatalf("tunnel %q not found or not published", name)
		}
	} else {
		// Dial the tunnel using rstream dialer (HTTP/1.1)
		url = rstream.StringPtr("ws://" + name + "/websocket")
		dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil || host == "" {
				return nil, fmt.Errorf("failed to extract host from address: %v", err)
			}
			return client.Dial(ctx, rstream.Addr{IdOrName: host})
		}
	}
	// Connect to the WebSocket server
	conn, _, err := dialer.DialContext(ctx, *url, nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	return handleConnection(conn)
}

func main() {
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	tunnel := flag.String("tunnel", "ws-example", "tunnel name (ws-example, ws-h2c-example, ws-h3-example)")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, client, *publish, *tunnel); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}
