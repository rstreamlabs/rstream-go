// See LICENSE file in the project root for license information.

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func payload(size, sequence int) []byte {
	p := bytes.Repeat([]byte{0xa5}, size)
	if len(p) >= 8 {
		binary.BigEndian.PutUint64(p[:8], uint64(sequence))
	}
	return p
}

func checkDuration(elapsed, minDuration, maxDuration time.Duration) error {
	if minDuration > 0 && elapsed < minDuration {
		return fmt.Errorf("transfer completed in %s, below minimum %s", elapsed, minDuration)
	}
	if maxDuration > 0 && elapsed > maxDuration {
		return fmt.Errorf("transfer completed in %s, above maximum %s", elapsed, maxDuration)
	}
	return nil
}

func runBytestream(ctx context.Context, client *rstream.Client, name string, size, iterations int) error {
	conn, err := client.Dial(ctx, rstream.Addr{IdOrName: name})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	for i := 0; i < iterations; i++ {
		want := payload(size, i)
		if _, err := conn.Write(want); err != nil {
			return fmt.Errorf("write packet %d: %w", i, err)
		}
		got := make([]byte, len(want))
		if _, err := io.ReadFull(conn, got); err != nil {
			return fmt.Errorf("read packet %d: %w", i, err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("echo mismatch for packet %d", i)
		}
	}
	return nil
}

func packetPath(conn net.PacketConn) string {
	if addr := conn.LocalAddr(); addr != nil && addr.Network() == "rstrm" {
		return "quic-datagram"
	}
	return "stream"
}

func runDatagram(ctx context.Context, client *rstream.Client, name, expectedPath string, size, iterations int) error {
	raddr := rstream.Addr{IdOrName: name}
	conn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("packet dial: %w", err)
	}
	defer conn.Close()
	if expectedPath != "" && packetPath(conn) != expectedPath {
		return fmt.Errorf("packet path=%s, want %s", packetPath(conn), expectedPath)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	buf := make([]byte, size)
	for i := 0; i < iterations; i++ {
		want := payload(size, i)
		if _, err := conn.WriteTo(want, &raddr); err != nil {
			return fmt.Errorf("write packet %d: %w", i, err)
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return fmt.Errorf("read packet %d: %w", i, err)
		}
		if !bytes.Equal(buf[:n], want) {
			return fmt.Errorf("echo mismatch for packet %d", i)
		}
	}
	return nil
}

func main() {
	tunnelType := flag.String("type", "datagram", "tunnel type: bytestream or datagram")
	name := flag.String("name", "bandwidth-limit", "private tunnel name")
	expectedPath := flag.String("expect-tunnel-packet-path", "", "expected datagram path: stream or quic-datagram")
	size := flag.Int("payload-size", 1000, "application payload bytes per iteration")
	iterations := flag.Int("iterations", 256, "echo iterations")
	timeout := flag.Duration("timeout", 10*time.Second, "overall timeout")
	minDuration := flag.Duration("min-duration", 0, "minimum accepted transfer duration")
	maxDuration := flag.Duration("max-duration", 0, "maximum accepted transfer duration")
	flag.Parse()
	if *size < 8 || *size > 1100 || *iterations <= 0 {
		log.Fatal("payload-size must be between 8 and 1100 and iterations must be positive")
	}
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	start := time.Now()
	switch *tunnelType {
	case "bytestream":
		err = runBytestream(ctx, client, *name, *size, *iterations)
	case "datagram":
		err = runDatagram(ctx, client, *name, *expectedPath, *size, *iterations)
	default:
		err = fmt.Errorf("unsupported tunnel type %q", *tunnelType)
	}
	elapsed := time.Since(start)
	if err == nil {
		err = checkDuration(elapsed, *minDuration, *maxDuration)
	}
	if err != nil {
		fmt.Printf("FAIL bandwidth-%s (%s): %v\n", *tunnelType, elapsed, err)
		os.Exit(1)
	}
	fmt.Printf("PASS bandwidth-%s (%s, %d bytes each direction)\n", *tunnelType, elapsed, *size**iterations)
}
