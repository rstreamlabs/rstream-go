// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-go"
)

func handler(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	w.Header().Set("Server", "rstream-go-example/1.0")
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, hostname)
}

func run(ctx context.Context) error {
	// 1. Open control channel
	ctrl, err := (&rstream.Client{}).Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// 2. Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:        rstream.StringPtr("h1-example"),
		Publish:     rstream.BoolPtr(true),
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
	// 3. Start an HTTP server using the tunnel as a listener (HTTP/1.1)
	server := &http.Server{
		Handler: http.HandlerFunc(handler),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(netListener)
	}()
	select {
	case <-ctx.Done():
		log.Println("Shutting down HTTP server...")
		return server.Shutdown(context.Background())
	case err := <-errCh:
		return fmt.Errorf("http server error: %w", err)
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal, exiting...")
		cancel()
	}()
	if err := run(ctx); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
