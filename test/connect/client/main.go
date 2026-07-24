// See LICENSE file in the project root for license information.

// connect-runtime-client opens a plain HTTP CONNECT tunnel through a published
// rstream HTTP tunnel and verifies a bidirectional TCP echo exchange.

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
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

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	rstream "github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"golang.org/x/net/http2"
)

const payload = "connect-runtime-ping"

func hostFromAddr(addr string) (string, error) {
	addr = strings.TrimRight(strings.TrimSpace(addr), " /")
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

func resolveForwarding(client *rstream.Client, name, addr string) (string, error) {
	if addr != "" {
		return hostFromAddr(addr)
	}
	host, err := findTunnelHost(client, name)
	if err != nil {
		return "", err
	}
	return hostFromAddr(host)
}

func verifyEcho(conn io.ReadWriteCloser) error {
	defer conn.Close()
	if _, err := conn.Write([]byte(payload)); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fmt.Errorf("read echo: %w", err)
	}
	if !bytes.Equal(buf, []byte(payload)) {
		return fmt.Errorf("echo mismatch: got %q want %q", string(buf), payload)
	}
	return nil
}

func dialH1(ctx context.Context, forwarding, target string) (io.ReadWriteCloser, error) {
	forwardingHost, _, err := net.SplitHostPort(forwarding)
	if err != nil {
		return nil, fmt.Errorf("split forwarding: %w", err)
	}
	dialer := &tls.Dialer{
		Config: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"http/1.1"},
			ServerName:         forwardingHost,
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", forwarding)
	if err != nil {
		return nil, fmt.Errorf("dial TLS: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		Proto:  "HTTP/1.1",
		Header: http.Header{"User-Agent": {""}},
		Host:   target,
		URL:    &url.URL{Scheme: "https", Host: target},
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write CONNECT: %w", err)
	}
	br := bufio.NewReaderSize(conn, 512)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = conn.Close()
		return nil, fmt.Errorf("CONNECT status %s", resp.Status)
	}
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: br}, nil
	}
	return conn, nil
}

func dialH2(ctx context.Context, forwarding, target string) (io.ReadWriteCloser, error) {
	forwardingHost, _, err := net.SplitHostPort(forwarding)
	if err != nil {
		return nil, fmt.Errorf("split forwarding: %w", err)
	}
	pr, pw := io.Pipe()
	tr := &http2.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}},
		DialTLSContext: func(ctx context.Context, _, _ string, cfg *tls.Config) (net.Conn, error) {
			cfg = cfg.Clone()
			cfg.ServerName = forwardingHost
			dialer := &tls.Dialer{Config: cfg}
			return dialer.DialContext(ctx, "tcp", forwarding)
		},
	}
	req := &http.Request{
		Method:        http.MethodConnect,
		Proto:         "HTTP/2.0",
		Header:        http.Header{"User-Agent": {""}},
		Host:          target,
		URL:           &url.URL{Scheme: "https", Host: target},
		Body:          pr,
		ContentLength: -1,
	}
	req = req.WithContext(ctx)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return nil, fmt.Errorf("CONNECT over H2 failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = pw.Close()
		_ = pr.Close()
		_ = resp.Body.Close()
		return nil, fmt.Errorf("CONNECT status %s", resp.Status)
	}
	return &h2ConnectStream{reader: resp.Body, writer: pw, tr: tr}, nil
}

func dialH3(ctx context.Context, forwarding, target string) (io.ReadWriteCloser, error) {
	host, port, err := net.SplitHostPort(forwarding)
	if err != nil {
		return nil, fmt.Errorf("split forwarding: %w", err)
	}
	udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("resolve UDP: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, fmt.Errorf("listen UDP: %w", err)
	}
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{http3.NextProtoH3},
		ServerName:         host,
	}
	qconn, err := quic.DialEarly(ctx, udpConn, udpAddr, tlsCfg, &quic.Config{})
	if err != nil {
		_ = udpConn.Close()
		return nil, fmt.Errorf("QUIC dial: %w", err)
	}
	go func() {
		<-qconn.Context().Done()
		_ = udpConn.Close()
	}()
	h3tr := &http3.Transport{}
	conn := h3tr.NewClientConn(qconn)
	requestStr, err := conn.OpenRequestStream(ctx)
	if err != nil {
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
		return nil, fmt.Errorf("open H3 request stream: %w", err)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		Proto:  "HTTP/1.1",
		Header: http.Header{"User-Agent": {""}},
		Host:   target,
		URL:    &url.URL{Scheme: "https", Host: target},
	}
	req = req.WithContext(ctx)
	if err := requestStr.SendRequestHeader(req); err != nil {
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
		return nil, fmt.Errorf("send H3 CONNECT headers: %w", err)
	}
	resp, err := requestStr.ReadResponse()
	if err != nil {
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
		return nil, fmt.Errorf("read H3 CONNECT response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
		return nil, fmt.Errorf("CONNECT status %s", resp.Status)
	}
	return &h3ConnectStream{str: requestStr, conn: conn}, nil
}

func run(ctx context.Context, rstreamClient *rstream.Client, downstream, name, addr, target string) error {
	if target == "" {
		return errors.New("--target is required")
	}
	forwarding, err := resolveForwarding(rstreamClient, name, addr)
	if err != nil {
		return err
	}
	var conn io.ReadWriteCloser
	switch downstream {
	case "h2":
		conn, err = dialH2(ctx, forwarding, target)
	case "h3":
		conn, err = dialH3(ctx, forwarding, target)
	default:
		conn, err = dialH1(ctx, forwarding, target)
	}
	if err != nil {
		return err
	}
	if err := verifyEcho(conn); err != nil {
		return err
	}
	fmt.Printf("CONNECT %s echo ok\n", downstream)
	return nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.reader.Buffered() > 0 {
		return c.reader.Read(p)
	}
	return c.Conn.Read(p)
}

type h2ConnectStream struct {
	reader io.ReadCloser
	writer *io.PipeWriter
	tr     *http2.Transport
}

func (s *h2ConnectStream) Read(p []byte) (int, error)  { return s.reader.Read(p) }
func (s *h2ConnectStream) Write(p []byte) (int, error) { return s.writer.Write(p) }
func (s *h2ConnectStream) Close() error {
	err1 := s.writer.Close()
	err2 := s.reader.Close()
	s.tr.CloseIdleConnections()
	if err1 != nil {
		return err1
	}
	return err2
}

type h3ConnectStream struct {
	str  *http3.RequestStream
	conn *http3.ClientConn
}

func (s *h3ConnectStream) Read(p []byte) (int, error)  { return s.str.Read(p) }
func (s *h3ConnectStream) Write(p []byte) (int, error) { return s.str.Write(p) }
func (s *h3ConnectStream) Close() error {
	s.str.CancelRead(quic.StreamErrorCode(http3.ErrCodeNoError))
	s.str.CancelWrite(quic.StreamErrorCode(http3.ErrCodeNoError))
	_ = s.str.Close()
	_ = s.conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
	return nil
}

func main() {
	downstream := flag.String("downstream", "h1", "downstream protocol: h1, h2, h3")
	name := flag.String("name", "connect-runtime", "tunnel name")
	addr := flag.String("addr", "", "published forwarding address")
	target := flag.String("target", "", "CONNECT target host:port")
	flag.Parse()
	if *downstream == "h3" {
		os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
	}
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, client, *downstream, *name, *addr, *target); err != nil {
		log.Fatalf("client error: %v", err)
	}
}
