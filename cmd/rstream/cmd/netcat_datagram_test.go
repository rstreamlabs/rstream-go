// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
)

func netcatFrameBytes(t *testing.T, payloads ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	for _, p := range payloads {
		if err := writeNetcatFrame(w, []byte(p)); err != nil {
			t.Fatalf("writeNetcatFrame(%q) error = %v", p, err)
		}
	}
	return b.Bytes()
}

func newNetcatTestUDPConn(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestNetcatFrameRoundTrip(t *testing.T) {
	data := netcatFrameBytes(t, "alpha", "", "beta")
	r := bufio.NewReader(bytes.NewReader(data))
	buf := make([]byte, maxNetcatFrameSize)
	for _, want := range []string{"alpha", "", "beta"} {
		n, err := readNetcatFrame(r, buf)
		if err != nil {
			t.Fatalf("readNetcatFrame() error = %v", err)
		}
		if string(buf[:n]) != want {
			t.Fatalf("readNetcatFrame() = %q, want %q", buf[:n], want)
		}
	}
	if _, err := readNetcatFrame(r, buf); !errors.Is(err, io.EOF) {
		t.Fatalf("readNetcatFrame() at end error = %v, want io.EOF", err)
	}
	if err := writeNetcatFrame(bufio.NewWriter(io.Discard), make([]byte, maxNetcatFrameSize+1)); err == nil {
		t.Fatalf("writeNetcatFrame() accepted an oversized datagram")
	}
}

func TestRunNetcatDatagramSessionBridgesFrames(t *testing.T) {
	connA := newNetcatTestUDPConn(t)
	connB := newNetcatTestUDPConn(t)
	outR, outW := io.Pipe()
	session := &netcatDatagramSession{
		Conn:       connA,
		RemoteAddr: connB.LocalAddr(),
		In:         bytes.NewReader(netcatFrameBytes(t, "alpha", "beta")),
		Out:        outW,
		Logger:     slog.Default(),
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sessionErrCh := make(chan error, 1)
	go func() { sessionErrCh <- runNetcatDatagramSession(ctx, session) }()
	buf := make([]byte, 2048)
	if err := connB.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	for _, want := range []string{"alpha", "beta"} {
		n, _, err := connB.ReadFrom(buf)
		if err != nil {
			t.Fatalf("ReadFrom() error = %v", err)
		}
		if string(buf[:n]) != want {
			t.Fatalf("received datagram %q, want %q", buf[:n], want)
		}
	}
	if _, err := connB.WriteTo([]byte("gamma"), connA.LocalAddr()); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	frameBuf := make([]byte, maxNetcatFrameSize)
	n, err := readNetcatFrame(bufio.NewReader(outR), frameBuf)
	if err != nil {
		t.Fatalf("readNetcatFrame() from session output error = %v", err)
	}
	if string(frameBuf[:n]) != "gamma" {
		t.Fatalf("session output frame = %q, want gamma", frameBuf[:n])
	}
	cancel()
	if err := <-sessionErrCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("session error = %v, want context.Canceled", err)
	}
}

func TestRunNetcatDatagramSessionJoinsContextInputReader(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	var startedOnce sync.Once
	var stoppedOnce sync.Once
	session := &netcatDatagramSession{
		Conn:       &netcatFakePacketConn{},
		RemoteAddr: netcatTestAddr("remote"),
		In:         strings.NewReader(""),
		InReadContext: func(ctx context.Context, _ []byte) (int, error) {
			startedOnce.Do(func() { close(started) })
			<-ctx.Done()
			stoppedOnce.Do(func() { close(stopped) })
			return 0, ctx.Err()
		},
		Out:    io.Discard,
		Logger: slog.Default(),
	}
	if err := runNetcatDatagramSession(t.Context(), session); err != nil {
		t.Fatalf("runNetcatDatagramSession() error = %v", err)
	}
	select {
	case <-started:
	default:
		t.Fatal("input reader did not start")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("runNetcatDatagramSession returned before its input reader stopped")
	}
}

func TestNetcatDatagramRecvLoopIdleTimeout(t *testing.T) {
	conn := newNetcatTestUDPConn(t)
	start := time.Now()
	if err := netcatDatagramRecvLoop(conn, io.Discard, 50*time.Millisecond, slog.Default()); err != nil {
		t.Fatalf("netcatDatagramRecvLoop() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("idle timeout returned too early after %v", elapsed)
	}
}

type netcatFakePacketConn struct {
	writeErr error
	writes   [][]byte
}

func (c *netcatFakePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	return 0, nil, net.ErrClosed
}

func (c *netcatFakePacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.writeErr != nil {
		err := c.writeErr
		c.writeErr = nil
		return 0, err
	}
	c.writes = append(c.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (c *netcatFakePacketConn) Close() error                       { return nil }
func (c *netcatFakePacketConn) LocalAddr() net.Addr                { return netcatTestAddr("local") }
func (c *netcatFakePacketConn) SetDeadline(time.Time) error        { return nil }
func (c *netcatFakePacketConn) SetReadDeadline(time.Time) error    { return nil }
func (c *netcatFakePacketConn) SetWriteDeadline(t time.Time) error { return nil }

func TestNetcatDatagramSendLoopDropsOversizedDatagrams(t *testing.T) {
	conn := &netcatFakePacketConn{writeErr: fmt.Errorf("%w: 2000 bytes", rstream.ErrDatagramTooLarge)}
	in := bytes.NewReader(netcatFrameBytes(t, "dropped", "kept"))
	if err := netcatDatagramSendLoop(conn, netcatTestAddr("remote"), in, slog.Default()); err != nil {
		t.Fatalf("netcatDatagramSendLoop() error = %v", err)
	}
	if len(conn.writes) != 1 || string(conn.writes[0]) != "kept" {
		t.Fatalf("unexpected forwarded datagrams: %q", conn.writes)
	}
}

func TestNetcatDatagramSendLoopRejectsTruncatedFrame(t *testing.T) {
	conn := &netcatFakePacketConn{}
	err := netcatDatagramSendLoop(conn, netcatTestAddr("remote"), bytes.NewReader([]byte{0x00}), slog.Default())
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("netcatDatagramSendLoop() error = %v, want truncated frame error", err)
	}
}

func TestRunNetcatDatagramClientClosesTransport(t *testing.T) {
	closed := false
	cfg := &netcatClientConfig{
		Target: "rstrm://media",
		PacketDial: func(context.Context) (net.PacketConn, net.Addr, error) {
			return &netcatFakePacketConn{}, netcatTestAddr("remote"), nil
		},
		CloseTransport: func() error {
			closed = true
			return nil
		},
		Logger: slog.Default(),
	}
	if err := runNetcatDatagramClient(t.Context(), cfg); err != nil {
		t.Fatalf("runNetcatDatagramClient() error = %v", err)
	}
	if !closed {
		t.Fatalf("transport was not closed")
	}
}

func TestRunNetcatDatagramClientRejectsUncancelableStdinBeforeDial(t *testing.T) {
	dialed := false
	err := runNetcatDatagramClient(t.Context(), &netcatClientConfig{
		Interactive: true,
		Stdin:       uncancelableNetcatReader{},
		PacketDial: func(context.Context) (net.PacketConn, net.Addr, error) {
			dialed = true
			return nil, nil, errors.New("unexpected dial")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stdin reader must support cancellation") {
		t.Fatalf("runNetcatDatagramClient() error = %v, want cancellable stdin error", err)
	}
	if dialed {
		t.Fatal("runNetcatDatagramClient dialed before validating stdin cancellation")
	}
}

func TestRunNetcatDatagramServerClosesTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	closed := false
	cfg := &netcatServerConfig{
		UDPListen: "127.0.0.1:0",
		PacketDial: func(context.Context) (net.PacketConn, net.Addr, error) {
			return nil, nil, errors.New("unexpected dial")
		},
		CloseTransport: func() error {
			closed = true
			return nil
		},
		Logger: slog.Default(),
	}
	if err := runNetcatDatagramServer(ctx, cfg); err != nil {
		t.Fatalf("runNetcatDatagramServer() error = %v", err)
	}
	if !closed {
		t.Fatalf("transport was not closed")
	}
}

func TestNewNetcatConfigsRejectDatagramTCPEndpoints(t *testing.T) {
	command := newTestNetcatCommand()
	mustSetFlag(t, command, "datagram", "true")
	if _, err := newNetcatClientConfig(command, slog.Default(), "127.0.0.1:9"); err == nil || !strings.Contains(err.Error(), "rstream endpoint") {
		t.Fatalf("expected datagram TCP client rejection, got %v", err)
	}
	command = newTestNetcatCommand()
	mustSetFlag(t, command, "listen", "127.0.0.1:0")
	mustSetFlag(t, command, "datagram", "true")
	mustSetFlag(t, command, "sh-exec", "cat")
	if _, err := newNetcatServerConfig(command, slog.Default()); err == nil || !strings.Contains(err.Error(), "listen endpoint") {
		t.Fatalf("expected datagram TCP listen rejection, got %v", err)
	}
}

func TestNetcatPacketHelpersRequireRstreamClient(t *testing.T) {
	dialer := newNetcatPacketDialer(netcatDialTarget{Kind: netcatEndpointRstream, Address: "media"}, nil)
	if _, _, err := dialer(t.Context()); err == nil || !strings.Contains(err.Error(), "rstream client is required") {
		t.Fatalf("expected missing client error from packet dialer, got %v", err)
	}
	factory := newNetcatPacketListenerFactory(netcatListenTarget{Kind: netcatEndpointRstream, Name: rstream.StringPtr("media")}, nil, nil)
	if _, err := factory(t.Context()); err == nil || !strings.Contains(err.Error(), "rstream client is required") {
		t.Fatalf("expected missing client error from packet listener factory, got %v", err)
	}
}

func TestNewNetcatClientConfigParsesDatagramAndExec(t *testing.T) {
	command := newTestNetcatCommand()
	mustSetFlag(t, command, "sh-exec", "printf ok")
	cfg, err := newNetcatClientConfig(command, slog.Default(), "127.0.0.1:9")
	if err != nil {
		t.Fatalf("newNetcatClientConfig() error = %v", err)
	}
	if cfg.Exec == nil || cfg.Exec.Command != "printf ok" || !cfg.Exec.Shell || cfg.Datagram {
		t.Fatalf("unexpected exec client config: %#v", cfg)
	}
}
