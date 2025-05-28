// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"fmt"
	"log"
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
	// 1. Create rstream listener
	listener := rstream.Listener{
		TunnelProperties: &rstream.TunnelProperties{
			Name:        rstream.StringPtr("h1-example"),
			Publish:     rstream.BoolPtr(true),
			Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
			HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
		},
		OnListenerInfo: func(info rstream.ListenerInfo) {
			status := rstream.StrOrUndef(info.Status)
			tunnelID := "undefined"
			if props := info.TunnelProperties; props != nil {
				tunnelID = rstream.StrOrUndef(props.ID)
			}
			forwarding := rstream.StrOrUndef(info.ForwardingAddress)
			log.Printf("[OnListenerInfo] Status: %s, Tunnel ID: %s, Forwarding Address: %s",
				status, tunnelID, forwarding)
		},
	}
	defer listener.Close()
	// 2. Create the HTTP server (HTTP/1.1)
	server := &http.Server{
		Handler: http.HandlerFunc(handler),
	}
	// 3. Run the server using the previously created listener
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(&listener)
	}()
	// 5. Wait for context cancellation
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
