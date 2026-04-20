// See LICENSE file in the project root for license information.

// stream-matrix-client is the client side of the end-to-end stream coverage
// matrix. For each variant, it dials the server via the rstream SDK dialer,
// sends a fixed payload, reads it back, and checks for exact round-trip.

package main

import (
	"context"
	"crypto/tls"
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

const streamPayload = "ping-stream\n"

func runPlain(ctx context.Context, client *rstream.Client, namePrefix string) error {
	conn, err := client.Dial(ctx, rstream.Addr{IdOrName: namePrefix + "-plain"})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	return echoCheck(conn)
}

func runTLS(ctx context.Context, client *rstream.Client, namePrefix string) error {
	inner, err := client.Dial(ctx, rstream.Addr{IdOrName: namePrefix + "-tls"})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	conn := tls.Client(inner, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}})
	defer conn.Close()
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("tls handshake: %w", err)
	}
	return echoCheck(conn)
}

func echoCheck(conn net.Conn) error {
	payload := []byte(streamPayload)
	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if string(got) != streamPayload {
		return fmt.Errorf("echo mismatch: got %q, want %q", string(got), streamPayload)
	}
	return nil
}

type testCase struct {
	name string
	run  func(ctx context.Context) error
}

func main() {
	variant := flag.String("variant", "all", "variant: plain, tls, all")
	namePrefix := flag.String("tunnel", "stream-matrix", "tunnel name prefix")
	timeout := flag.Duration("timeout", 30*time.Second, "per-case timeout")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	prefix := *namePrefix
	allCases := []testCase{
		{
			name: "plain",
			run: func(ctx context.Context) error {
				return runPlain(ctx, client, prefix)
			},
		},
		{
			name: "tls",
			run: func(ctx context.Context) error {
				return runTLS(ctx, client, prefix)
			},
		},
	}
	var cases []testCase
	if *variant == "all" {
		cases = allCases
	} else {
		for _, tc := range allCases {
			if tc.name == *variant {
				cases = append(cases, tc)
				break
			}
		}
		if len(cases) == 0 {
			log.Fatalf("unknown variant %q (known: plain, tls, all)", *variant)
		}
	}
	var failed []string
	for _, tc := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		start := time.Now()
		runErr := tc.run(ctx)
		elapsed := time.Since(start)
		cancel()
		if runErr != nil {
			failed = append(failed, tc.name)
			fmt.Printf("FAIL %-8s (%.2fs): %v\n", tc.name, elapsed.Seconds(), runErr)
		} else {
			fmt.Printf("PASS %-8s (%.2fs)\n", tc.name, elapsed.Seconds())
		}
	}
	fmt.Printf("---- summary: %d passed, %d failed out of %d ----\n", len(cases)-len(failed), len(failed), len(cases))
	if len(failed) > 0 {
		os.Exit(1)
	}
}
