// See LICENSE file in the project root for license information.

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

func run(ctx context.Context, publish bool) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	name := "ws-example"
	var url *string = nil
	if publish {
		// List tunnels to find the published host using rstream API (data plane)
		tunnels, err := (&rstream.Client{}).ListTunnels(context.Background(), nil)
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
		url = rstream.StringPtr("ws://" + name + "/websocket")
		// Dial the tunnel using rstream dialer (HTTP/1.1)
		dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil || host == "" {
				return nil, fmt.Errorf("failed to extract host from address: %v", err)
			}
			return (&rstream.Client{}).Dial(ctx, rstream.Addr{IdOrName: host})
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
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx, *publish); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}
