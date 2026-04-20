// See LICENSE file in the project root for license information.

// http-ws-server starts a WebSocket server behind an rstream tunnel using
// gorilla/websocket. It replies "pong" to "ping" and echoes everything else.
// Any compliant WebSocket client reaches it through rstream with no changes to
// the server code — rstream handles public exposure transparently.
//
// Run: go run . (internal only) or go run . -publish (published WS endpoint)

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func run(ctx context.Context, client *rstream.Client, publish bool, publishedProtocol string) error {
	// Open control channel
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("ws-example"),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		switch strings.ToLower(strings.TrimSpace(publishedProtocol)) {
		case "http":
			tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
			tunnelProps.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP1_1)
		case "tls":
			tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolTLS)
		default:
			return fmt.Errorf("invalid published protocol %q (expected http or tls)", publishedProtocol)
		}
	}
	tunnel, err := ctrl.CreateTunnel(ctx, tunnelProps)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer tunnel.Close()
	forwardingAddr, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("failed to get forwarding address: %w", err)
	}
	netListener, ok := tunnel.(interface{ net.Listener })
	if !ok {
		return fmt.Errorf("tunnel does not implement net.Listener")
	}
	fmt.Printf("Server listening on %s\n", forwardingAddr)
	// Start a WebSocket server using the tunnel as a listener
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux}
	mux.HandleFunc("/websocket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if string(msg) == "ping" {
				if err := conn.WriteMessage(mt, []byte("pong")); err != nil {
					return
				}
			} else {
				if err := conn.WriteMessage(mt, msg); err != nil {
					return
				}
			}
		}
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(netListener)
	}()
	select {
	case <-ctx.Done():
		log.Println("Shutting down WebSocket server...")
		return server.Shutdown(context.Background())
	case err := <-errCh:
		return fmt.Errorf("websocket server error: %w", err)
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel")
	publishedProtocol := flag.String("published-protocol", "http", "published edge protocol to use when -publish=true (http or tls)")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, exiting...")
		cancel()
	}()
	if err := run(ctx, client, *publish, *publishedProtocol); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
