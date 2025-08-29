// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go"
)

func run(ctx context.Context) error {
	// 1. Create the WebSocket dialer (HTTP/1.1)
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 5 * time.Second,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil || host == "" {
				return nil, fmt.Errorf("failed to extract host from address: %v", err)
			}
			return (&rstream.Client{}).Dial(ctx, rstream.Addr{IdOrName: host})
		},
	}
	// 2. Connect to the WebSocket server
	conn, _, err := dialer.DialContext(ctx, "ws://ws-example/", nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}
	defer conn.Close()
	// 3. Send a ping and read the response
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

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := run(ctx); err != nil {
		log.Fatalf("Client error: %v", err)
	}
}
