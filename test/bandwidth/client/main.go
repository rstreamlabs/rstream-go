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

func writePackets(conn net.Conn, size, iterations int) error {
	for i := 0; i < iterations; i++ {
		packet := payload(size, i)
		for len(packet) > 0 {
			n, err := conn.Write(packet)
			if n > 0 {
				packet = packet[n:]
			}
			if err != nil {
				return fmt.Errorf("write packet %d: %w", i, err)
			}
			if n == 0 {
				return fmt.Errorf("write packet %d: %w", i, io.ErrNoProgress)
			}
		}
	}
	return nil
}

func exchangeBytestream(conn net.Conn, size, iterations int) error {
	written := make(chan error, 1)
	go func() { written <- writePackets(conn, size, iterations) }()
	got := make([]byte, size)
	for i := 0; i < iterations; i++ {
		if _, err := io.ReadFull(conn, got); err != nil {
			_ = conn.Close()
			<-written
			return fmt.Errorf("read packet %d: %w", i, err)
		}
		if !bytes.Equal(got, payload(size, i)) {
			_ = conn.Close()
			<-written
			return fmt.Errorf("echo mismatch for packet %d", i)
		}
	}
	if err := <-written; err != nil {
		return err
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
	return exchangeBytestream(conn, size, iterations)
}

func packetPath(conn net.PacketConn) string {
	if addr := conn.LocalAddr(); addr != nil && addr.Network() == "rstrm" {
		return "quic-datagram"
	}
	return "stream"
}

func exchangeDatagrams(conn net.PacketConn, raddr net.Addr, size, iterations, window int) error {
	slots := make(chan struct{}, window)
	done := make(chan struct{})
	written := make(chan error, 1)
	go func() {
		for i := 0; i < iterations; i++ {
			select {
			case slots <- struct{}{}:
			case <-done:
				written <- net.ErrClosed
				return
			}
			if _, err := conn.WriteTo(payload(size, i), raddr); err != nil {
				_ = conn.Close()
				written <- fmt.Errorf("write packet %d: %w", i, err)
				return
			}
		}
		written <- nil
	}()
	buf := make([]byte, size)
	seen := make([]bool, iterations)
	received := 0
	for received < iterations {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			close(done)
			_ = conn.Close()
			<-written
			return fmt.Errorf("read packet %d: %w", received, err)
		}
		if n != size {
			close(done)
			_ = conn.Close()
			<-written
			return fmt.Errorf("echo size mismatch: got %d, want %d", n, size)
		}
		sequence := int(binary.BigEndian.Uint64(buf[:8]))
		if sequence < 0 || sequence >= iterations || !bytes.Equal(buf[:n], payload(size, sequence)) {
			close(done)
			_ = conn.Close()
			<-written
			return fmt.Errorf("invalid echo payload sequence %d", sequence)
		}
		if seen[sequence] {
			continue
		}
		seen[sequence] = true
		received++
		<-slots
	}
	close(done)
	if err := <-written; err != nil {
		return err
	}
	return nil
}

func runDatagram(ctx context.Context, client *rstream.Client, name, expectedPath string, size, iterations, window int) error {
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
	return exchangeDatagrams(conn, &raddr, size, iterations, window)
}

func main() {
	tunnelType := flag.String("type", "datagram", "tunnel type: bytestream or datagram")
	name := flag.String("name", "bandwidth-limit", "private tunnel name")
	expectedPath := flag.String("expect-tunnel-packet-path", "", "expected datagram path: stream or quic-datagram")
	size := flag.Int("payload-size", 1000, "application payload bytes per iteration")
	iterations := flag.Int("iterations", 256, "echo iterations")
	datagramWindow := flag.Int("datagram-window", 16, "maximum datagrams awaiting an echo")
	timeout := flag.Duration("timeout", 10*time.Second, "overall timeout")
	minDuration := flag.Duration("min-duration", 0, "minimum accepted transfer duration")
	maxDuration := flag.Duration("max-duration", 0, "maximum accepted transfer duration")
	flag.Parse()
	if *size < 8 || *size > 1100 || *iterations <= 0 || *datagramWindow <= 0 || *datagramWindow > *iterations {
		log.Fatal("payload-size, iterations, and datagram-window are outside their supported bounds")
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
		err = runDatagram(ctx, client, *name, *expectedPath, *size, *iterations, *datagramWindow)
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
