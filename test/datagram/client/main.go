// See LICENSE file in the project root for license information.

// datagram-matrix-client is the client side of the end-to-end datagram coverage
// matrix. It connects to the server, sends test payloads, and verifies the
// echo round-trip.
//
// Without --addr the rstream SDK dialer is used (unpublished path).
// With --addr the client connects directly to the engine's edge endpoint:
// pion/dtls for the dtls and sctp variants, quic-go for the quic variant.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/sctp"
	"github.com/quic-go/quic-go"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

const quicEchoALPN = "rstream-datagram-echo"
const tunneledQUICInitialPacketSize = 1200

func quicALPNs(tlsALPN string) []string {
	if tlsALPN != "" {
		return []string{tlsALPN}
	}
	return []string{quicEchoALPN}
}

func dtlsALPNs(tlsALPN string) []string {
	if tlsALPN != "" {
		return []string{tlsALPN}
	}
	return nil
}

func dtlsClientOptions(hostname, tlsALPN string) []dtls.ClientOption {
	opts := []dtls.ClientOption{dtls.WithServerName(hostname), dtls.WithInsecureSkipVerify(true)}
	if protocols := dtlsALPNs(tlsALPN); len(protocols) > 0 {
		opts = append(opts, dtls.WithSupportedProtocols(protocols...))
	}
	return opts
}

// hostPortFromAddr extracts a bare host:port, stripping any scheme prefix and
// trailing protocol annotation (e.g. " (dtls)", " (quic)").
func hostPortFromAddr(addr, defaultPort string) string {
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
		addr = net.JoinHostPort(addr, defaultPort)
	}
	return addr
}

// ── DTLS ──────────────────────────────────────────────────────────────────────

func checkTunnelPacketPath(conn net.PacketConn, expected string) error {
	if expected == "" {
		return nil
	}
	localNetwork := ""
	if addr := conn.LocalAddr(); addr != nil {
		localNetwork = addr.Network()
	}
	actual := "stream"
	if localNetwork == "rstrm" {
		actual = "quic-datagram"
	}
	if actual != expected {
		return fmt.Errorf("tunnel packet path = %s (local network %q), want %s", actual, localNetwork, expected)
	}
	return nil
}

func runDTLSUnpublished(ctx context.Context, client *rstream.Client, tunnelName, expectedPath string) error {
	raddr := rstream.Addr{IdOrName: tunnelName}
	packetConn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("PacketDial: %w", err)
	}
	defer packetConn.Close()
	if err := checkTunnelPacketPath(packetConn, expectedPath); err != nil {
		return err
	}
	return dtlsEcho(packetConn, &raddr)
}

func runDTLSPublished(ctx context.Context, addr, tlsALPN string) error {
	hp := hostPortFromAddr(addr, "4433")
	hostname, _, _ := net.SplitHostPort(hp)
	udpAddr, err := net.ResolveUDPAddr("udp", hp)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", hp, err)
	}
	conn, err := dtls.DialWithOptions("udp", udpAddr, dtlsClientOptions(hostname, tlsALPN)...)
	if err != nil {
		return fmt.Errorf("dtls dial %s: %w", hp, err)
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("dtls handshake %s: %w", hp, err)
	}
	if tlsALPN != "" {
		if state, ok := conn.ConnectionState(); !ok || state.NegotiatedProtocol != tlsALPN {
			return fmt.Errorf("unexpected DTLS ALPN: got %q, want %q", state.NegotiatedProtocol, tlsALPN)
		}
	}
	pc := rstream.PacketConnFromDTLSConn(conn)
	defer pc.Close()
	return dtlsEcho(pc, udpAddr)
}

func dtlsEcho(pc net.PacketConn, raddr net.Addr) error {
	payload := []byte("ping-dtls")
	if _, err := pc.WriteTo(payload, raddr); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	buf := make([]byte, 2048)
	if err := pc.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		return fmt.Errorf("echo mismatch: got %q, want %q", buf[:n], payload)
	}
	return nil
}

// ── QUIC ──────────────────────────────────────────────────────────────────────

func runQUICUnpublished(ctx context.Context, client *rstream.Client, tunnelName, expectedPath string) error {
	raddr := rstream.Addr{IdOrName: tunnelName}
	packetConn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("PacketDial: %w", err)
	}
	defer packetConn.Close()
	if err := checkTunnelPacketPath(packetConn, expectedPath); err != nil {
		return err
	}
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	transport := quic.Transport{Conn: packetConn}
	conn, err := transport.Dial(ctx, &raddr,
		&tls.Config{InsecureSkipVerify: true, NextProtos: quicALPNs("")},
		&quic.Config{EnableDatagrams: true, InitialPacketSize: tunneledQUICInitialPacketSize})
	if err != nil {
		return fmt.Errorf("quic dial: %w", err)
	}
	return quicEcho(ctx, conn)
}

func runQUICPublished(ctx context.Context, addr, tlsALPN string) error {
	hp := hostPortFromAddr(addr, "443")
	hostname, _, _ := net.SplitHostPort(hp)
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	conn, err := quic.DialAddr(ctx, hp,
		&tls.Config{ServerName: hostname, InsecureSkipVerify: true, NextProtos: quicALPNs(tlsALPN)},
		&quic.Config{EnableDatagrams: true})
	if err != nil {
		return fmt.Errorf("quic dial %s: %w", hp, err)
	}
	if tlsALPN != "" && conn.ConnectionState().TLS.NegotiatedProtocol != tlsALPN {
		return fmt.Errorf("unexpected QUIC ALPN: got %q, want %q", conn.ConnectionState().TLS.NegotiatedProtocol, tlsALPN)
	}
	return quicEcho(ctx, conn)
}

func quicEcho(ctx context.Context, conn *quic.Conn) error {
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

// ── SCTP ──────────────────────────────────────────────────────────────────────

func runSCTPUnpublished(ctx context.Context, client *rstream.Client, tunnelName, expectedPath string) error {
	raddr := rstream.Addr{IdOrName: tunnelName}
	packetConn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return fmt.Errorf("PacketDial: %w", err)
	}
	if err := checkTunnelPacketPath(packetConn, expectedPath); err != nil {
		packetConn.Close()
		return err
	}
	return sctpEcho(rstream.ConnFromPacketConn(packetConn, &raddr))
}

func runSCTPPublished(ctx context.Context, addr, tlsALPN string) error {
	hp := hostPortFromAddr(addr, "4433")
	hostname, _, _ := net.SplitHostPort(hp)
	udpAddr, err := net.ResolveUDPAddr("udp", hp)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", hp, err)
	}
	conn, err := dtls.DialWithOptions("udp", udpAddr, dtlsClientOptions(hostname, tlsALPN)...)
	if err != nil {
		return fmt.Errorf("dtls dial %s: %w", hp, err)
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return fmt.Errorf("dtls handshake %s: %w", hp, err)
	}
	if tlsALPN != "" {
		if state, ok := conn.ConnectionState(); !ok || state.NegotiatedProtocol != tlsALPN {
			conn.Close()
			return fmt.Errorf("unexpected DTLS ALPN: got %q, want %q", state.NegotiatedProtocol, tlsALPN)
		}
	}
	return sctpEcho(conn)
}

func sctpEcho(conn net.Conn) error {
	defer conn.Close()
	assoc, err := sctp.ClientWithOptions(sctp.WithNetConn(conn))
	if err != nil {
		return fmt.Errorf("sctp client: %w", err)
	}
	defer assoc.Close()
	stream, err := assoc.OpenStream(0, sctp.PayloadTypeWebRTCString)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()
	payload := []byte("ping-sctp")
	if err := stream.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return fmt.Errorf("set stream deadline: %w", err)
	}
	if _, err := stream.Write(payload); err != nil {
		return fmt.Errorf("stream write: %w", err)
	}
	buf := make([]byte, 2048)
	n, err := stream.Read(buf)
	if err != nil {
		return fmt.Errorf("stream read: %w", err)
	}
	if !bytes.Equal(buf[:n], payload) {
		return fmt.Errorf("stream echo mismatch: got %q, want %q", buf[:n], payload)
	}
	return nil
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	variant := flag.String("variant", "dtls", "variant: dtls, quic, sctp")
	addr := flag.String("addr", "", "forwarding address for direct (published) connection")
	tlsALPN := flag.String("tls-alpn", "", "custom ALPN for published DTLS, QUIC, or SCTP tunnels")
	tunnelPrefix := flag.String("tunnel", "datagram-matrix", "tunnel name prefix for SDK dialer")
	expectedTunnelPacketPath := flag.String("expect-tunnel-packet-path", "", "expected private tunnel packet path: stream or quic-datagram")
	timeout := flag.Duration("timeout", 30*time.Second, "per-case timeout")
	flag.Parse()
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
	tctx, tcancel := context.WithTimeout(ctx, *timeout)
	defer tcancel()
	label := *variant
	if *addr != "" {
		label += "-published"
	}
	start := time.Now()
	var runErr error
	switch *variant {
	case "dtls":
		if *addr != "" {
			runErr = runDTLSPublished(tctx, *addr, *tlsALPN)
		} else {
			runErr = runDTLSUnpublished(tctx, client, *tunnelPrefix+"-dtls", *expectedTunnelPacketPath)
		}
	case "quic":
		if *addr != "" {
			runErr = runQUICPublished(tctx, *addr, *tlsALPN)
		} else {
			runErr = runQUICUnpublished(tctx, client, *tunnelPrefix+"-quic", *expectedTunnelPacketPath)
		}
	case "sctp":
		if *addr != "" {
			runErr = runSCTPPublished(tctx, *addr, *tlsALPN)
		} else {
			runErr = runSCTPUnpublished(tctx, client, *tunnelPrefix+"-sctp", *expectedTunnelPacketPath)
		}
	default:
		log.Fatalf("unknown variant %q: must be dtls, quic, or sctp", *variant)
	}
	elapsed := time.Since(start)
	if runErr != nil {
		fmt.Printf("FAIL %-20s (%.2fs): %v\n", label, elapsed.Seconds(), runErr)
		os.Exit(1)
	}
	fmt.Printf("PASS %-20s (%.2fs)\n", label, elapsed.Seconds())
}
