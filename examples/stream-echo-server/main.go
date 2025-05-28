// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-go"
)

func handleConnection(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 2048)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err != io.EOF {
				log.Printf("Read error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}
		log.Printf("Received %d bytes from %s: %s\n", n, conn.RemoteAddr(), buf[:n])
		if _, err := conn.Write(buf[:n]); err != nil {
			log.Printf("Write error to %s: %v", conn.RemoteAddr(), err)
			return
		}
	}
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
		Name:    rstream.StringPtr("stream-echo"),
		Type:    rstream.TunnelTypePtr(rstream.TunnelBytestream),
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
	listener, ok := tunnel.(net.Listener)
	if !ok {
		return fmt.Errorf("tunnel does not implement net.Listener")
	}
	// 3. Echo server
	for {
		conn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("listener accept error: %w", err)
		}
		go handleConnection(conn)
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
