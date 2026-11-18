// See LICENSE file in the project root for license information.

// masque-client connects to masque-server through an rstream DatagramTunnel and
// verifies a CONNECT-UDP or CONNECT-IP round trip. By default it uses the
// rstream SDK internal datagram dialer. With -publish, it connects to the
// server's published HTTP/3 endpoint.
//
// Run: go run . (rstream dialer) or go run . -publish (published H3 endpoint)

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
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	connectip "github.com/quic-go/connect-ip-go"
	"github.com/quic-go/masque-go"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/yosida95/uritemplate/v3"
)

const (
	connectIPClientAddr = "10.77.0.2"
	connectIPServerAddr = "10.77.0.1"
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

func publishedEndpoint(ctx context.Context, client *rstream.Client, name, addr string) (hostport, baseURL string, err error) {
	if strings.TrimSpace(addr) != "" {
		return normalizePublishedAddr(addr)
	}
	tunnels, err := client.ListTunnels(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to list tunnels: %w", err)
	}
	for _, tunnel := range *tunnels {
		if tunnel.Name != nil && *tunnel.Name == name && tunnel.Host != nil {
			return normalizePublishedAddr(*tunnel.Host)
		}
	}
	return "", "", fmt.Errorf("tunnel %q not found or not published", name)
}

func dialH3(ctx context.Context, client *rstream.Client, publish bool, name, addr string) (*http3.ClientConn, string, func(), error) {
	if publish {
		hostport, baseURL, err := publishedEndpoint(ctx, client, name, addr)
		if err != nil {
			return nil, "", nil, err
		}
		qconn, err := quic.DialAddr(ctx, hostport, h3TLSConfig(hostWithoutPort(hostport)), h3QUICConfig())
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to dial published HTTP/3 endpoint: %w", err)
		}
		h3conn := (&http3.Transport{EnableDatagrams: true}).NewClientConn(qconn)
		return h3conn, baseURL, func() {
			_ = h3conn.CloseWithError(0, "client done")
			_ = qconn.CloseWithError(0, "client done")
		}, nil
	}
	raddr := rstream.Addr{IdOrName: name}
	packetConn, err := client.PacketDial(ctx, raddr)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to dial tunnel: %w", err)
	}
	qconn, err := quic.DialEarly(ctx, packetConn, &raddr, h3TLSConfig(name), h3QUICConfig())
	if err != nil {
		_ = packetConn.Close()
		return nil, "", nil, fmt.Errorf("failed to dial HTTP/3 over tunnel: %w", err)
	}
	h3conn := (&http3.Transport{EnableDatagrams: true}).NewClientConn(qconn)
	return h3conn, "https://" + name, func() {
		_ = h3conn.CloseWithError(0, "client done")
		_ = qconn.CloseWithError(0, "client done")
		_ = packetConn.Close()
	}, nil
}

func h3TLSConfig(serverName string) *tls.Config {
	return &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true,
		NextProtos:         []string{http3.NextProtoH3},
	}
}

func h3QUICConfig() *quic.Config {
	return &quic.Config{EnableDatagrams: true}
}

func runConnectUDP(ctx context.Context, client *rstream.Client, publish bool, name, addr, target string) error {
	if target == "" {
		return errors.New("--target host:port is required for connect-udp")
	}
	if publish {
		_, baseURL, err := publishedEndpoint(ctx, client, name, addr)
		if err != nil {
			return err
		}
		tpl, err := uritemplate.New(baseURL + "/.well-known/masque/udp/{target_host}/{target_port}/")
		if err != nil {
			return err
		}
		request, err := masque.NewRequest(ctx, tpl, target)
		if err != nil {
			return err
		}
		transport := &masque.Transport{
			TLSClientConfig: h3TLSConfig(hostWithoutPort(strings.TrimPrefix(baseURL, "https://"))),
			QUICConfig:      h3QUICConfig(),
		}
		pc, resp, err := transport.Dial(request)
		if err != nil {
			if resp != nil {
				return fmt.Errorf("CONNECT-UDP status %s: %w", resp.Status, err)
			}
			return err
		}
		defer pc.Close()
		return verifyConnectUDPEcho(ctx, pc)
	}
	h3conn, baseURL, closeH3, err := dialH3(ctx, client, publish, name, addr)
	if err != nil {
		return err
	}
	defer closeH3()
	return runConnectUDPOverH3(ctx, h3conn, baseURL, target)
}

func runConnectUDPOverH3(ctx context.Context, h3conn *http3.ClientConn, baseURL, target string) error {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("failed to parse target: %w", err)
	}
	tpl, err := uritemplate.New(baseURL + "/.well-known/masque/udp/{target_host}/{target_port}/")
	if err != nil {
		return err
	}
	expanded, err := tpl.Expand(uritemplate.Values{
		"target_host": uritemplate.String(host),
		"target_port": uritemplate.String(port),
	})
	if err != nil {
		return fmt.Errorf("failed to expand CONNECT-UDP template: %w", err)
	}
	u, err := url.Parse(expanded)
	if err != nil {
		return fmt.Errorf("failed to parse CONNECT-UDP URI: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h3conn.Context().Done():
		return context.Cause(h3conn.Context())
	case <-h3conn.ReceivedSettings():
	}
	settings := h3conn.Settings()
	if !settings.EnableExtendedConnect {
		return errors.New("server did not enable Extended CONNECT")
	}
	if !settings.EnableDatagrams {
		return errors.New("server did not enable HTTP/3 datagrams")
	}
	stream, err := h3conn.OpenRequestStream(ctx)
	if err != nil {
		return fmt.Errorf("failed to open CONNECT-UDP stream: %w", err)
	}
	defer stream.Close()
	if err := stream.SendRequestHeader(&http.Request{
		Method: http.MethodConnect,
		Proto:  "connect-udp",
		Host:   u.Host,
		Header: http.Header{http3.CapsuleProtocolHeader: []string{"?1"}},
		URL:    u,
	}); err != nil {
		return fmt.Errorf("failed to send CONNECT-UDP request: %w", err)
	}
	resp, err := stream.ReadResponse()
	if err != nil {
		return fmt.Errorf("failed to read CONNECT-UDP response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("CONNECT-UDP status %s", resp.Status)
	}
	payload := []byte("ping-connect-udp")
	datagram := quicvarint.Append(nil, 0)
	datagram = append(datagram, payload...)
	if err := stream.SendDatagram(datagram); err != nil {
		return fmt.Errorf("write UDP payload: %w", err)
	}
	reply, err := stream.ReceiveDatagram(ctx)
	if err != nil {
		return fmt.Errorf("read UDP echo: %w", err)
	}
	contextID, n, err := quicvarint.Parse(reply)
	if err != nil {
		return fmt.Errorf("malformed CONNECT-UDP datagram: %w", err)
	}
	if contextID != 0 {
		return fmt.Errorf("unexpected CONNECT-UDP context ID %d", contextID)
	}
	if !bytes.Equal(reply[n:], payload) {
		return fmt.Errorf("echo mismatch: got %q, want %q", reply[n:], payload)
	}
	fmt.Printf("CONNECT-UDP echo ok: %s\n", payload)
	return nil
}

func verifyConnectUDPEcho(ctx context.Context, pc net.PacketConn) error {
	payload := []byte("ping-connect-udp")
	if _, err := pc.WriteTo(payload, nil); err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(10 * time.Second)
	}
	if err := pc.SetReadDeadline(deadline); err != nil {
		return err
	}
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		return err
	}
	if !bytes.Equal(buf[:n], payload) {
		return fmt.Errorf("echo mismatch: got %q, want %q", buf[:n], payload)
	}
	fmt.Printf("CONNECT-UDP echo ok: %s\n", payload)
	return nil
}

func runConnectIP(ctx context.Context, client *rstream.Client, publish bool, name, addr string) error {
	h3conn, baseURL, closeH3, err := dialH3(ctx, client, publish, name, addr)
	if err != nil {
		return err
	}
	defer closeH3()
	tpl, err := uritemplate.New(baseURL + "/connect-ip")
	if err != nil {
		return err
	}
	conn, resp, err := connectip.Dial(ctx, h3conn, tpl)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("CONNECT-IP status %s: %w", resp.Status, err)
		}
		return err
	}
	defer conn.Close()
	if _, err := conn.LocalPrefixes(ctx); err != nil {
		return err
	}
	if _, err := conn.Routes(ctx); err != nil {
		return err
	}
	packet := buildIPv4Packet(netip.MustParseAddr(connectIPClientAddr), netip.MustParseAddr(connectIPServerAddr))
	if icmp, err := conn.WritePacket(packet); err != nil {
		return err
	} else if len(icmp) > 0 {
		return fmt.Errorf("unexpected local ICMP response of %d bytes", len(icmp))
	}
	buf := make([]byte, 1500)
	done := make(chan error, 1)
	go func() {
		n, err := conn.ReadPacket(buf)
		if err != nil {
			done <- err
			return
		}
		done <- verifyIPv4Echo(buf[:n])
	}()
	select {
	case err := <-done:
		if err != nil {
			return err
		}
		fmt.Println("CONNECT-IP packet echo ok")
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("timed out waiting for CONNECT-IP echo")
	case <-ctx.Done():
		return ctx.Err()
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
	variant := flag.String("variant", "connect-udp", "connect-udp or connect-ip")
	name := flag.String("name", "masque-example", "rstream tunnel name")
	publish := flag.Bool("publish", false, "connect to published host instead of using rstream dialer")
	addr := flag.String("addr", "", "published rstream endpoint override")
	target := flag.String("target", "", "UDP target host:port for connect-udp")
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	switch strings.ToLower(strings.TrimSpace(*variant)) {
	case "connect-udp":
		err = runConnectUDP(ctx, client, *publish, *name, *addr, *target)
	case "connect-ip":
		err = runConnectIP(ctx, client, *publish, *name, *addr)
	default:
		err = fmt.Errorf("invalid variant %q", *variant)
	}
	if err != nil {
		log.Fatalf("Client error: %v", err)
	}
}
