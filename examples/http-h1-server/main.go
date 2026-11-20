// See LICENSE file in the project root for license information.

// http-h1-server starts a minimal HTTP/1.1 server behind an rstream
// BytestreamTunnel that returns the server hostname on every GET request.
// rstream exposes the server at a TLS-terminated public endpoint so clients
// need no VPN or SSH tunnel to reach it.
//
// Run: go run . (internal only) or go run . -publish (published HTTP endpoint)

package main

import (
	"context"
	"errors"
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

func handler(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	w.Header().Set("Server", "rstream-go-example/1.0")
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, hostname)
}

func run(ctx context.Context, publish bool) error {
	client, err := config.NewClientFromEnv()
	if err != nil {
		return fmt.Errorf("load rstream configuration: %w", err)
	}
	defer client.Close()
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("h1-example"),
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
	netListener, ok := tunnel.(net.Listener)
	if !ok {
		return fmt.Errorf("tunnel does not implement net.Listener")
	}
	fmt.Printf("Server listening on %s\n", forwardingAddr)
	server := &http.Server{Handler: http.HandlerFunc(handler)}
	defer server.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(netListener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return fmt.Errorf("http server error: %w", err)
	}
}

func main() {
	publish := flag.Bool("publish", false, "publish the tunnel")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, *publish)
	stop()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server error: %v", err)
	}
}
