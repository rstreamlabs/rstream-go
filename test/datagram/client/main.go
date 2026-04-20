// See LICENSE file in the project root for license information.

// datagram-matrix-client is the client side of the end-to-end datagram coverage
// matrix. It dials each variant via the rstream SDK, sends test payloads, and
// verifies the echo round-trip.
//
// Variants: dtls (UDP echo via PacketDial), quic (quic-go stream + datagram echo).

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

const quicEchoALPN = "rstream-datagram-echo"

type testCase struct {
	name    string
	variant string
}

func listNames(prefix, variant string) []testCase {
	all := []testCase{
		{name: prefix + "-dtls", variant: "dtls"},
		{name: prefix + "-quic", variant: "quic"},
	}
	if variant == "all" {
		return all
	}
	for _, tc := range all {
		if tc.variant == variant {
			return []testCase{tc}
		}
	}
	return nil
}

func runDTLS(ctx context.Context, client *rstream.Client, tunnelName string) error {
	raddr := rstream.Addr{IdOrName: tunnelName}
	packetConn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("PacketDial: %w", err)
	}
	defer packetConn.Close()
	payload := []byte("ping-dtls")
	if _, err := packetConn.WriteTo(payload, &raddr); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	buf := make([]byte, 2048)
	n, _, err := packetConn.ReadFrom(buf)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		return fmt.Errorf("echo mismatch: got %q, want %q", buf[:n], payload)
	}
	return nil
}

func runQUIC(ctx context.Context, client *rstream.Client, tunnelName string) error {
	raddr := rstream.Addr{IdOrName: tunnelName}
	packetConn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("PacketDial: %w", err)
	}
	defer packetConn.Close()
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{quicEchoALPN},
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	transport := quic.Transport{Conn: packetConn}
	conn, err := transport.Dial(ctx, &raddr, tlsCfg, &quic.Config{EnableDatagrams: true})
	if err != nil {
		return fmt.Errorf("quic dial: %w", err)
	}
	defer conn.CloseWithError(0, "client done")
	stream, err := conn.OpenStream()
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()
	streamPayload := []byte("ping-quic")
	if _, err := stream.Write(streamPayload); err != nil {
		return fmt.Errorf("stream write: %w", err)
	}
	buf := make([]byte, 2048)
	n, err := stream.Read(buf)
	if err != nil {
		return fmt.Errorf("stream read: %w", err)
	}
	if !bytes.Equal(buf[:n], streamPayload) {
		return fmt.Errorf("stream echo mismatch: got %q, want %q", buf[:n], streamPayload)
	}
	dgPayload := []byte("dg-quic")
	if err := conn.SendDatagram(dgPayload); err != nil {
		return fmt.Errorf("send datagram: %w", err)
	}
	dgCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	echo, err := conn.ReceiveDatagram(dgCtx)
	if err != nil {
		return fmt.Errorf("receive datagram: %w", err)
	}
	if !bytes.Equal(echo, dgPayload) {
		return fmt.Errorf("datagram echo mismatch: got %q, want %q", echo, dgPayload)
	}
	return nil
}

func main() {
	variant := flag.String("variant", "all", "variant to run: dtls, quic, or all")
	tunnelPrefix := flag.String("tunnel", "datagram-matrix", "tunnel name prefix")
	timeout := flag.Duration("timeout", 30*time.Second, "per-case timeout")
	flag.Parse()
	cases := listNames(*tunnelPrefix, *variant)
	if len(cases) == 0 {
		log.Fatalf("unknown variant %q: must be dtls, quic, or all", *variant)
	}
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()
	pass := 0
	fail := 0
	for _, tc := range cases {
		tctx, tcancel := context.WithTimeout(ctx, *timeout)
		var runErr error
		switch tc.variant {
		case "dtls":
			runErr = runDTLS(tctx, client, tc.name)
		case "quic":
			runErr = runQUIC(tctx, client, tc.name)
		}
		tcancel()
		if runErr != nil {
			fmt.Printf("FAIL [%s]: %v\n", tc.variant, runErr)
			fail++
		} else {
			fmt.Printf("PASS [%s]\n", tc.variant)
			pass++
		}
	}
	fmt.Printf("\n%d passed, %d failed\n", pass, fail)
	if fail > 0 {
		os.Exit(1)
	}
}
