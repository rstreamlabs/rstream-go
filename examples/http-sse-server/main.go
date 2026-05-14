// See LICENSE file in the project root for license information.

// http-sse-server serves Server-Sent Events behind an rstream HTTP tunnel. The
// handler is a normal net/http SSE endpoint; rstream only provides the private
// or published tunnel transport.
//
// Run: go run . (internal only) or go run . -publish (published HTTP endpoint)

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
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher := http.NewResponseController(w)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for i := 1; i <= 5; i++ {
		select {
		case <-r.Context().Done():
			return
		case now := <-ticker.C:
			fmt.Fprintf(w, "id: %d\nevent: tick\ndata: %s\n\n", i, now.Format(time.RFC3339))
			if err := flusher.Flush(); err != nil {
				return
			}
		}
	}
}

func run(ctx context.Context, client *rstream.Client, publish bool) error {
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("sse-example"),
		Publish: rstream.BoolPtr(publish),
	}
	if publish {
		tunnelProps.Protocol = rstream.ProtocolPtr(rstream.ProtocolHTTP)
		tunnelProps.HTTPVersion = rstream.HTTPVersionPtr(rstream.HTTP1_1)
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
	mux := http.NewServeMux()
	mux.HandleFunc("/events", eventsHandler)
	server := &http.Server{Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(netListener)
	}()
	fmt.Printf("SSE server listening on %s/events\n", forwardingAddr)
	select {
	case <-ctx.Done():
		log.Println("Shutting down SSE server...")
		return server.Shutdown(context.Background())
	case err := <-errCh:
		return fmt.Errorf("http server error: %w", err)
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel")
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
	if err := run(ctx, client, *publish); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
