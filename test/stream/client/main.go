// See LICENSE file in the project root for license information.

// stream-matrix-client is the client side of the end-to-end stream coverage
// matrix. It dials the server, sends a fixed payload, reads it back, and
// checks for an exact round-trip.
//
// Without --addr the rstream SDK dialer is used (unpublished path).
// With --addr the client connects directly to the engine's edge endpoint.
// For the tls variant, the direct path uses tls.Dial.

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
	"strings"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

const streamPayload = "ping-stream\n"

// hostPortFromAddr extracts a bare host:port from an address that may carry
// a scheme prefix (e.g. "tls://host:443" → "host:443") or a trailing protocol
// annotation (e.g. "host:443 (tls)" → "host:443").
func hostPortFromAddr(addr string) string {
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		addr = addr[:i]
	}
	if i := strings.IndexByte(addr, ' '); i >= 0 {
		addr = addr[:i]
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "443")
	}
	return addr
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

func runPlain(ctx context.Context, client *rstream.Client, tunnelName string) error {
	conn, err := client.Dial(ctx, rstream.Addr{IdOrName: tunnelName})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	return echoCheck(conn)
}

func tlsNextProtos(tlsALPN string) []string {
	if tlsALPN != "" {
		return []string{tlsALPN}
	}
	return []string{"http/1.1"}
}

func runTLSUnpublished(ctx context.Context, client *rstream.Client, tunnelName, tlsALPN string) error {
	inner, err := client.Dial(ctx, rstream.Addr{IdOrName: tunnelName})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	conn := tls.Client(inner, &tls.Config{InsecureSkipVerify: true, NextProtos: tlsNextProtos(tlsALPN)})
	defer conn.Close()
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("tls handshake: %w", err)
	}
	if tlsALPN != "" && conn.ConnectionState().NegotiatedProtocol != tlsALPN {
		return fmt.Errorf("unexpected ALPN: got %q, want %q", conn.ConnectionState().NegotiatedProtocol, tlsALPN)
	}
	return echoCheck(conn)
}

func runTLSPublished(ctx context.Context, addr, tlsALPN string) error {
	hp := hostPortFromAddr(addr)
	host, _, _ := net.SplitHostPort(hp)
	conn, err := tls.DialWithDialer(
		&net.Dialer{},
		"tcp", hp,
		&tls.Config{ServerName: host, InsecureSkipVerify: true, NextProtos: tlsNextProtos(tlsALPN)},
	)
	if err != nil {
		return fmt.Errorf("tls dial %s: %w", hp, err)
	}
	defer conn.Close()
	if tlsALPN != "" && conn.ConnectionState().NegotiatedProtocol != tlsALPN {
		return fmt.Errorf("unexpected ALPN: got %q, want %q", conn.ConnectionState().NegotiatedProtocol, tlsALPN)
	}
	return echoCheck(conn)
}

type testCase struct {
	name string
	run  func(ctx context.Context) error
}

func main() {
	variant := flag.String("variant", "plain", "variant: plain, tls")
	addr := flag.String("addr", "", "forwarding address for direct (published) connection")
	tlsALPN := flag.String("tls-alpn", "", "custom ALPN for TLS connections")
	tunnelPrefix := flag.String("tunnel", "stream-matrix", "tunnel name prefix for SDK dialer")
	timeout := flag.Duration("timeout", 30*time.Second, "per-case timeout")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	var tc testCase
	switch *variant {
	case "plain":
		tc = testCase{
			name: "plain",
			run: func(ctx context.Context) error {
				return runPlain(ctx, client, *tunnelPrefix+"-plain")
			},
		}
	case "tls":
		if *addr != "" {
			tc = testCase{
				name: "tls-published",
				run: func(ctx context.Context) error {
					return runTLSPublished(ctx, *addr, *tlsALPN)
				},
			}
		} else {
			tc = testCase{
				name: "tls",
				run: func(ctx context.Context) error {
					return runTLSUnpublished(ctx, client, *tunnelPrefix+"-tls", *tlsALPN)
				},
			}
		}
	default:
		log.Fatalf("unknown variant %q: must be plain or tls", *variant)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	start := time.Now()
	runErr := tc.run(ctx)
	elapsed := time.Since(start)
	cancel()
	if runErr != nil {
		fmt.Printf("FAIL %-16s (%.2fs): %v\n", tc.name, elapsed.Seconds(), runErr)
		os.Exit(1)
	}
	fmt.Printf("PASS %-16s (%.2fs)\n", tc.name, elapsed.Seconds())
}
