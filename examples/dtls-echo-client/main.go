// See LICENSE file in the project root for license information.

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/pion/dtls/v3"
	"github.com/rstreamlabs/rstream-go"
)

func run(ctx context.Context) error {
	// 1. Dial the tunnel
	raddr := rstream.Addr{IdOrName: "dtls-echo"}
	packetConn, err := (&rstream.Client{}).PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("failed to dial tunnel: %w", err)
	}
	defer packetConn.Close()
	// 2. Connect to the DTLS server
	dtlsCfg := &dtls.Config{
		InsecureSkipVerify: true,
	}
	conn, err := dtls.Client(packetConn, &raddr, dtlsCfg)
	if err != nil {
		return fmt.Errorf("failed to dial DTLS server: %w", err)
	}
	// 3. Send a message
	msg := []byte("Hello from rstream-go!")
	n, err := conn.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}
	log.Printf("Wrote %d bytes: %s", n, msg)
	// 4. Receive a message
	buf := make([]byte, 2048)
	n, err = conn.Read(buf)
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
