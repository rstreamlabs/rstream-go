// See LICENSE file in the project root for license information.

// masque-runtime-client drives CONNECT-UDP / CONNECT-IP through a published
// rstream HTTP/3 datagram tunnel and verifies an end-to-end echo.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	connectip "github.com/quic-go/connect-ip-go"
	"github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/yosida95/uritemplate/v3"
)

const (
	connectIPClientAddr    = "10.77.0.2"
	connectIPServerAddr    = "10.77.0.1"
	connectUDPEchoTimeout  = 10 * time.Second
	connectUDPRetryTimeout = 250 * time.Millisecond
)

func normalizePublishedAddr(raw string) (hostport, baseURL string, err error) {
	value := strings.TrimSpace(raw)
	if i := strings.IndexByte(value, ' '); i >= 0 {
		value = value[:i]
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", "", err
		}
		if parsed.Host == "" {
			return "", "", fmt.Errorf("missing host in %q", raw)
		}
		hostport = parsed.Host
	} else {
		hostport = value
	}
	if _, _, err := net.SplitHostPort(hostport); err != nil {
		var addrErr *net.AddrError
		if errors.As(err, &addrErr) && addrErr.Err == "missing port in address" {
			hostport = net.JoinHostPort(hostport, "443")
		} else {
			return "", "", err
		}
	}
	return hostport, "https://" + hostport, nil
}

func runConnectUDP(ctx context.Context, addr, target string) error {
	if strings.TrimSpace(target) == "" {
		return errors.New("--target is required for connect-udp")
	}
	_, baseURL, err := normalizePublishedAddr(addr)
	if err != nil {
		return err
	}
	tpl, err := uritemplate.New(baseURL + "/.well-known/masque/udp/{target_host}/{target_port}/")
	if err != nil {
		return err
	}
	client := &masque.Client{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{http3.NextProtoH3}},
		QUICConfig:      &quic.Config{EnableDatagrams: true},
	}
	defer client.Close()
	pc, resp, err := client.DialAddr(ctx, tpl, target)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("CONNECT-UDP failed with status %s: %w", resp.Status, err)
		}
		return err
	}
	defer pc.Close()
	payload := []byte("ping-connect-udp")
	return exchangeConnectUDPPayload(pc, payload, connectUDPEchoTimeout, connectUDPRetryTimeout)
}

func exchangeConnectUDPPayload(pc net.PacketConn, payload []byte, timeout, retryTimeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 2048)
	attempts := 0
	for {
		attempts++
		if _, err := pc.WriteTo(payload, nil); err != nil {
			return fmt.Errorf("write UDP payload: %w", err)
		}
		readDeadline := time.Now().Add(retryTimeout)
		if readDeadline.After(deadline) {
			readDeadline = deadline
		}
		if err := pc.SetReadDeadline(readDeadline); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}
		n, _, err := pc.ReadFrom(buf)
		if err == nil {
			if !bytes.Equal(buf[:n], payload) {
				return fmt.Errorf("UDP echo mismatch: got %q, want %q", buf[:n], payload)
			}
			return nil
		}
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			return fmt.Errorf("read UDP echo: %w", err)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("read UDP echo after %d attempts: %w", attempts, err)
		}
	}
}

func runConnectIP(ctx context.Context, addr string) error {
	hostport, baseURL, err := normalizePublishedAddr(addr)
	if err != nil {
		return err
	}
	qconn, err := quic.DialAddr(ctx, hostport,
		&tls.Config{ServerName: hostWithoutPort(hostport), InsecureSkipVerify: true, NextProtos: []string{http3.NextProtoH3}},
		&quic.Config{EnableDatagrams: true},
	)
	if err != nil {
		return fmt.Errorf("QUIC dial: %w", err)
	}
	defer qconn.CloseWithError(0, "client done")
	h3conn := (&http3.Transport{EnableDatagrams: true}).NewClientConn(qconn)
	defer h3conn.CloseWithError(0, "client done")
	tpl, err := uritemplate.New(baseURL + "/connect-ip")
	if err != nil {
		return err
	}
	conn, resp, err := connectip.Dial(ctx, h3conn, tpl)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("CONNECT-IP failed with status %s: %w", resp.Status, err)
		}
		return err
	}
	defer conn.Close()
	if _, err := conn.LocalPrefixes(ctx); err != nil {
		return fmt.Errorf("waiting for address assignment: %w", err)
	}
	if _, err := conn.Routes(ctx); err != nil {
		return fmt.Errorf("waiting for route advertisement: %w", err)
	}
	packet := buildIPv4Packet(netip.MustParseAddr(connectIPClientAddr), netip.MustParseAddr(connectIPServerAddr))
	if icmp, err := conn.WritePacket(packet); err != nil {
		return fmt.Errorf("write IP packet: %w", err)
	} else if len(icmp) > 0 {
		return fmt.Errorf("unexpected local ICMP response of %d bytes", len(icmp))
	}
	buf := make([]byte, 1500)
	readCh := make(chan struct {
		n   int
		err error
	}, 1)
	go func() {
		n, err := conn.ReadPacket(buf)
		readCh <- struct {
			n   int
			err error
		}{n: n, err: err}
	}()
	select {
	case got := <-readCh:
		if got.err != nil {
			return fmt.Errorf("read IP echo: %w", got.err)
		}
		return verifyIPv4Echo(buf[:got.n])
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return errors.New("timed out waiting for IP echo")
	}
}

func hostWithoutPort(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return host
}

func buildIPv4Packet(src, dst netip.Addr) []byte {
	packet := make([]byte, 20)
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	packet[8] = 64
	packet[9] = 1
	copy(packet[12:16], src.AsSlice())
	copy(packet[16:20], dst.AsSlice())
	binary.BigEndian.PutUint16(packet[10:12], ipv4HeaderChecksum(packet))
	return packet
}

func verifyIPv4Echo(packet []byte) error {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return fmt.Errorf("expected IPv4 packet, got %d bytes", len(packet))
	}
	var srcRaw, dstRaw [4]byte
	copy(srcRaw[:], packet[12:16])
	copy(dstRaw[:], packet[16:20])
	src := netip.AddrFrom4(srcRaw)
	dst := netip.AddrFrom4(dstRaw)
	if src.String() != connectIPServerAddr || dst.String() != connectIPClientAddr {
		return fmt.Errorf("unexpected IP echo src/dst: %s -> %s", src, dst)
	}
	return nil
}

func ipv4HeaderChecksum(header []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(header); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(header[i : i+2]))
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func main() {
	variant := flag.String("variant", "connect-udp", "MASQUE variant: connect-udp or connect-ip")
	addr := flag.String("addr", "", "published rstream forwarding address")
	target := flag.String("target", "", "CONNECT-UDP target host:port")
	timeout := flag.Duration("timeout", 20*time.Second, "test timeout")
	flag.Parse()
	if strings.TrimSpace(*addr) == "" {
		log.Fatal("--addr is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var err error
	switch strings.ToLower(strings.TrimSpace(*variant)) {
	case "connect-udp":
		err = runConnectUDP(ctx, *addr, *target)
	case "connect-ip":
		err = runConnectIP(ctx, *addr)
	default:
		err = fmt.Errorf("invalid variant %q", *variant)
	}
	if err != nil {
		log.Fatalf("client error: %v", err)
	}
}
