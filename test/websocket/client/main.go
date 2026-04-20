// See LICENSE file in the project root for license information.

// ws-matrix-client is the client side of the WebSocket coverage matrix.
// It connects to a ws-matrix-server tunnel via the engine forwarding address
// and validates WebSocket echo through the engine reverse proxy.
//
// Pass --addr to specify the forwarding address returned by ws-matrix-server
// (e.g. "https://abc123.t.c.localhost.rstream.io"). The host is extracted and
// used for all three downstream modes:
//
//   - h1  : HTTP/1.1 WebSocket (gorilla) to wss://host/ws
//   - h2  : HTTP/2 Extended CONNECT (RFC 8441) to https://host/ws
//   - h3  : HTTP/3 Extended CONNECT (RFC 9220) to QUIC host:443 /ws
//
// All modes verify the echo "hello-ws" arrives unmodified.

package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"golang.org/x/net/http2"
)

const testPayload = "hello-ws"

// wsWriteMasked writes a masked WebSocket frame (client→server) to w.
func wsWriteMasked(w io.Writer, opcode byte, payload []byte) error {
	maskKey := [4]byte{0x37, 0xfa, 0x21, 0x3d}
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ maskKey[i%4]
	}
	hdr := []byte{0x80 | opcode}
	plen := len(payload)
	switch {
	case plen <= 125:
		hdr = append(hdr, byte(plen)|0x80)
	case plen <= 65535:
		hdr = append(hdr, 126|0x80, byte(plen>>8), byte(plen))
	default:
		hdr = append(hdr, 127|0x80, 0, 0, 0, 0,
			byte(plen>>24), byte(plen>>16), byte(plen>>8), byte(plen))
	}
	hdr = append(hdr, maskKey[:]...)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(masked)
	return err
}

// wsReadUnmasked reads an unmasked WebSocket frame (server→client) from r.
func wsReadUnmasked(r io.Reader) (opcode byte, payload []byte, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(r, hdr); err != nil {
		return
	}
	opcode = hdr[0] & 0x0f
	plen := int(hdr[1] & 0x7f)
	if plen == 126 {
		ext := make([]byte, 2)
		if _, err = io.ReadFull(r, ext); err != nil {
			return
		}
		plen = int(binary.BigEndian.Uint16(ext))
	} else if plen == 127 {
		ext := make([]byte, 8)
		if _, err = io.ReadFull(r, ext); err != nil {
			return
		}
		plen = int(binary.BigEndian.Uint64(ext))
	}
	payload = make([]byte, plen)
	_, err = io.ReadFull(r, payload)
	return
}

// hostFromAddr extracts "host:443" from a forwarding address like
// "https://foo.rstream.io" → "foo.rstream.io:443".
func hostFromAddr(addr string) (string, error) {
	addr = strings.TrimRight(addr, " /")
	u, err := url.Parse(addr)
	if err != nil {
		return "", fmt.Errorf("parse addr %q: %w", addr, err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("no host in addr %q", addr)
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(host, port), nil
}

func runH1(ctx context.Context, hostPort string) error {
	host, _, _ := net.SplitHostPort(hostPort)
	dialer := websocket.Dialer{
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
		HandshakeTimeout: 15 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, "wss://"+hostPort+"/ws", http.Header{"Host": {host}})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(testPayload)); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if string(msg) != testPayload {
		return fmt.Errorf("echo mismatch: got %q, want %q", msg, testPayload)
	}
	return nil
}

func runH2(ctx context.Context, hostPort string) error {
	host, _, _ := net.SplitHostPort(hostPort)
	tr := &http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         host,
			NextProtos:         []string{"h2"},
		},
	}
	pr, pw := io.Pipe()
	req := &http.Request{
		Method:        http.MethodConnect,
		URL:           &url.URL{Scheme: "https", Host: hostPort, Path: "/ws"},
		Header:        http.Header{":protocol": {"websocket"}},
		Body:          pr,
		ContentLength: -1,
		Proto:         "HTTP/2.0",
		Host:          host,
	}
	req = req.WithContext(ctx)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		_ = pw.Close()
		return fmt.Errorf("round trip: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	if err := wsWriteMasked(pw, 0x2, []byte(testPayload)); err != nil {
		return fmt.Errorf("write ws frame: %w", err)
	}
	opcode, msg, err := wsReadUnmasked(resp.Body)
	if err != nil {
		return fmt.Errorf("read ws frame: %w", err)
	}
	_ = pw.Close()
	if opcode != 0x2 {
		return fmt.Errorf("unexpected opcode: %x", opcode)
	}
	if string(msg) != testPayload {
		return fmt.Errorf("echo mismatch: got %q, want %q", msg, testPayload)
	}
	return nil
}

func runH3(ctx context.Context, hostPort string) error {
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	host, _, _ := net.SplitHostPort(hostPort)
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         host,
		NextProtos:         []string{http3.NextProtoH3},
	}
	udpAddr, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return fmt.Errorf("resolve udp: %w", err)
	}
	pc, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	qconn, err := quic.DialEarly(ctx, pc, udpAddr, tlsCfg, nil)
	if err != nil {
		_ = pc.Close()
		return fmt.Errorf("quic dial: %w", err)
	}
	defer func() {
		_ = qconn.CloseWithError(0, "")
		_ = pc.Close()
	}()
	h3tr := &http3.Transport{}
	cc := h3tr.NewClientConn(qconn)
	select {
	case <-cc.ReceivedSettings():
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for H3 SETTINGS: %w", ctx.Err())
	}
	if !cc.Settings().EnableExtendedConnect {
		return fmt.Errorf("server did not advertise ExtendedConnect")
	}
	pr, pw := io.Pipe()
	req := &http.Request{
		Method:        http.MethodConnect,
		URL:           &url.URL{Scheme: "https", Host: host, Path: "/ws"},
		Proto:         "websocket",
		Body:          pr,
		ContentLength: -1,
		Host:          host,
	}
	req = req.WithContext(ctx)
	resp, err := cc.RoundTrip(req)
	if err != nil {
		_ = pw.Close()
		return fmt.Errorf("round trip: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	if err := wsWriteMasked(pw, 0x2, []byte(testPayload)); err != nil {
		return fmt.Errorf("write ws frame: %w", err)
	}
	opcode, msg, err := wsReadUnmasked(resp.Body)
	if err != nil {
		return fmt.Errorf("read ws frame: %w", err)
	}
	_ = pw.Close()
	if opcode != 0x2 {
		return fmt.Errorf("unexpected opcode: %x", opcode)
	}
	if string(msg) != testPayload {
		return fmt.Errorf("echo mismatch: got %q, want %q", msg, testPayload)
	}
	return nil
}

// findTunnelHost resolves a tunnel's forwarding host via the rstream API.
func findTunnelHost(client *rstream.Client, name string) (string, error) {
	tunnels, err := client.ListTunnels(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("list tunnels: %w", err)
	}
	for _, t := range *tunnels {
		if t.Name != nil && *t.Name == name && t.Host != nil {
			return *t.Host, nil
		}
	}
	return "", fmt.Errorf("tunnel %q not found or has no host", name)
}

func main() {
	downstream := flag.String("downstream", "h3", "downstream protocol: h1, h2, h3")
	addr := flag.String("addr", "", "forwarding address from server (e.g. https://host)")
	tunnelName := flag.String("tunnel", "", "tunnel name to resolve via API (alternative to --addr)")
	flag.Parse()
	var hostPort string
	if *addr != "" {
		var err error
		hostPort, err = hostFromAddr(*addr)
		if err != nil {
			log.Fatalf("addr: %v", err)
		}
	} else if *tunnelName != "" {
		client, err := config.NewClientFromEnv()
		if err != nil {
			log.Fatalf("config: %v", err)
		}
		host, err := findTunnelHost(client, *tunnelName)
		if err != nil {
			log.Fatalf("find tunnel: %v", err)
		}
		hostPort = net.JoinHostPort(host, "443")
	} else {
		log.Fatalf("must supply --addr or --tunnel")
	}
	tctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var err error
	switch *downstream {
	case "h1":
		err = runH1(tctx, hostPort)
	case "h2":
		err = runH2(tctx, hostPort)
	case "h3":
		err = runH3(tctx, hostPort)
	default:
		log.Fatalf("unknown downstream %q", *downstream)
	}
	if err != nil {
		fmt.Printf("FAIL [downstream=%s]: %v\n", *downstream, err)
		os.Exit(1)
	}
	fmt.Printf("PASS [downstream=%s]\n", *downstream)
}
