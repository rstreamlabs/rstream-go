// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/quic-go/quic-go"
	"github.com/rstreamlabs/rstream-go"
)

func run(ctx context.Context) error {
	// 1. Dial the tunnel
	raddr := rstream.Addr{IdOrName: "quic-echo"}
	packetConn, err := (&rstream.Client{}).PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("failed to dial tunnel: %w", err)
	}
	defer packetConn.Close()
	// 2. Connect to the QUIC server
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
	}
	transport := quic.Transport{
		Conn: packetConn,
	}
	conn, err := transport.Dial(ctx, &raddr, tlsCfg, nil)
	if err != nil {
		return fmt.Errorf("failed to dial QUIC server: %w", err)
	}
	defer conn.CloseWithError(0, "client done")
	// 3. Open a single stream
	stream, err := conn.OpenStream()
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()
	// 4. Send a message
	msg := []byte("Hello from rstream-go!")
	n, err := stream.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	log.Printf("Wrote %d bytes: %s", n, msg)
	// 5. Receive a message
	buf := make([]byte, 2048)
	n, err = stream.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read: %w", err)
	}
	log.Printf("Received %d bytes: %s", n, buf[:n])
	return nil
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
