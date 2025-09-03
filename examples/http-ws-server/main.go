// See LICENSE file in the project root for license information.

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
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/rstreamlabs/rstream-go"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func run(ctx context.Context, publish bool) error {
	// Open control channel
	ctrl, err := (&rstream.Client{}).Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:        rstream.StringPtr("ws-example"),
		Publish:     rstream.BoolPtr(publish),
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
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
	// Start a WebSocket server using the tunnel as a listener (HTTP/1.1)
	mux := http.NewServeMux()
	server := &http.Server{
		Handler: mux,
	}
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
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, exiting...")
		cancel()
	}()
	if err := run(ctx, *publish); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
