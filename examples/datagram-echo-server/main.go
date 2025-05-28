// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-go"
)

func run(ctx context.Context) error {
	// 1. Open control channel
	ctrl, err := (&rstream.Client{}).Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to rstream engine server: %w", err)
	}
	defer ctrl.Close()
	// 2. Create the tunnel
	tunnelProps := rstream.TunnelProperties{
		Name:    rstream.StringPtr("datagram-echo"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelDatagram),
		Publish: rstream.BoolPtr(false),
	}
	tunnel, err := ctrl.CreateTunnel(ctx, tunnelProps)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	go func() {
		<-ctx.Done()
		tunnel.Close()
	}()
	packetConn, ok := tunnel.(net.PacketConn)
	if !ok {
		return fmt.Errorf("tunnel does not implement net.PacketConn")
	}
	// 3. Echo server
	buf := make([]byte, 2048)
	for {
		n, addr, err := packetConn.ReadFrom(buf)
		if err != nil {
			return fmt.Errorf("readfrom error: %w", err)
		}
		log.Printf("Received %d bytes from %s: %s\n", n, addr, buf[:n])
		_, err = packetConn.WriteTo(buf[:n], addr)
		if err != nil {
			return fmt.Errorf("writeto error: %w", err)
		}
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Got signal, exiting...")
		cancel()
	}()
	if err := run(ctx); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
