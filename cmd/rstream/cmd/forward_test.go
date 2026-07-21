// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/spf13/cobra"
)

func TestForwardStatusTextAndConnectionOutput(t *testing.T) {
	var out bytes.Buffer
	details := &rstream.ServerDetails{
		Update:   rstream.StringPtr("available"),
		Plan:     rstream.StringPtr("pro"),
		Provider: rstream.StringPtr("aws"),
		Region:   rstream.StringPtr("eu-west-1"),
	}
	status := newForwardStatus(details)
	status.Status = rstream.StringPtr("online")
	status.TunnelID = rstream.StringPtr("tun-1")
	status.Forwarding = rstream.StringPtr("https://demo.example.com")
	status.Forwarded = rstream.StringPtr("localhost:8080")
	ctx := &forwardCtx{OutputFormat: forwardOutputFormatText, Out: &out, Logger: slog.Default()}
	ctx.setStatus(status)
	ip := net.IPv4(192, 0, 2, 10)
	ctx.addConn(forwardConnInfo{Active: true, Date: time.Date(2026, 5, 8, 1, 2, 3, 4*1e6, time.UTC), StreamID: rstream.StringPtr("stream-1"), SourceIP: &ip})
	ctx.closeConn(3)
	got := out.String()
	for _, want := range []string{"tunnel status", "online", "tun-1", "incoming connection", "192.0.2.10", "connection closed: idx=3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("text output missing %q in:\n%s", want, got)
		}
	}
}

func TestForwardJSONAndNoneOutput(t *testing.T) {
	var out bytes.Buffer
	ctx := &forwardCtx{OutputFormat: forwardOutputFormatJSON, Out: &out, Logger: slog.Default()}
	ctx.setStatus(forwardStatus{Status: rstream.StringPtr("connected")})
	ctx.addConn(forwardConnInfo{Active: false, Date: time.Unix(0, 0).UTC()})
	ctx.closeConn(2)
	got := out.String()
	for _, want := range []string{`"status":"connected"`, `"active":false`, `"event":"connection_closed"`, `"idx":2`} {
		if !strings.Contains(got, want) {
			t.Fatalf("json output missing %q in:\n%s", want, got)
		}
	}
	out.Reset()
	ctx.OutputFormat = forwardOutputFormatNone
	ctx.setStatus(forwardStatus{Status: rstream.StringPtr("hidden")})
	ctx.addConn(forwardConnInfo{Active: true, Date: time.Now()})
	ctx.closeConn(1)
	if out.Len() != 0 {
		t.Fatalf("none output wrote %q", out.String())
	}
}

func TestForwardStatusFormattingHelpers(t *testing.T) {
	if got := formatVersion("1.2.3", "dev"); got != "1.2.3 (dev)" {
		t.Fatalf("formatVersion(dev) = %q", got)
	}
	if got := formatVersion("1.2.3", "stable"); got != "1.2.3" {
		t.Fatalf("formatVersion(stable) = %q", got)
	}
	if got := formatStatusError("connection failed", nil); got != "connection failed" {
		t.Fatalf("formatStatusError(nil) = %q", got)
	}
	if got := formatStatusError("connection failed", errors.New("boom")); got != "connection failed (boom)" {
		t.Fatalf("formatStatusError(error) = %q", got)
	}
	err := errors.New("reported")
	wrapped := statusReportedError{err: err}
	if wrapped.Error() != "reported" || !errors.Is(wrapped, err) {
		t.Fatalf("statusReportedError did not wrap correctly")
	}
}

func TestForwardRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "network error", err: errors.New("connection reset"), want: true},
		{name: "wrapped service unavailable", err: fmt.Errorf("connect: %w", &rstream.EngineError{Code: rstream.EngineErrorCodeServiceUnavailable}), want: true},
		{name: "capacity exhausted", err: &rstream.EngineError{Code: rstream.EngineErrorCodeCapacityExhausted}, want: true},
		{name: "feature unavailable", err: statusReportedError{err: &rstream.EngineError{Code: rstream.EngineErrorCodeFeatureNotAvailable}}},
		{name: "invalid request", err: &rstream.EngineError{Code: rstream.EngineErrorCodeInvalidRequest}},
		{name: "context canceled", err: context.Canceled},
		{name: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forwardRetryableError(tt.err); got != tt.want {
				t.Fatalf("forwardRetryableError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestNewForwardCtxBuildsRuntimeAndOutputConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.WriteAtomic(path, config.Config{
		Defaults: config.Defaults{Context: &config.DefaultContext{Name: "dev"}},
		Contexts: []config.Context{{
			Name:   "dev",
			Engine: "engine.example.com:443",
			Auth: &config.Auth{Token: &config.Token{Storage: &config.TokenStorage{
				Kind:  config.TokenStorageInline,
				Value: "token",
			}}},
		}},
	}); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	command := forwardCtxTestCommand()
	mustSetFlag(t, command, "config", path)
	mustSetFlag(t, command, "output", "none")
	mustSetFlag(t, command, "no-retry", "true")
	mustSetFlag(t, command, "retry-interval", "1234")
	ctx, err := newForwardCtx(command, "localhost", "8080")
	if err != nil {
		t.Fatalf("newForwardCtx() error = %v", err)
	}
	if ctx.OutputFormat != forwardOutputFormatNone || ctx.AutoReconnect == nil || *ctx.AutoReconnect || ctx.ReconnectTimeout == nil || *ctx.ReconnectTimeout != 1234*time.Millisecond {
		t.Fatalf("unexpected forward ctx: %#v", ctx)
	}
	if ctx.Client == nil || ctx.Client.EngineURL == nil || *ctx.Client.EngineURL != "engine.example.com:443" {
		t.Fatalf("client not configured from runtime: %#v", ctx.Client)
	}
	command = forwardCtxTestCommand()
	mustSetFlag(t, command, "config", path)
	mustSetFlag(t, command, "output", "xml")
	if _, err := newForwardCtx(command, "localhost", "8080"); err == nil || !strings.Contains(err.Error(), "invalid output") {
		t.Fatalf("newForwardCtx(invalid output) = %v, want invalid output error", err)
	}
	command = forwardCtxTestCommand()
	mustSetFlag(t, command, "config", path)
	mustSetFlag(t, command, "retry-interval", "0")
	if _, err := newForwardCtx(command, "localhost", "8080"); err == nil || !strings.Contains(err.Error(), "--retry-interval") {
		t.Fatalf("newForwardCtx(invalid retry interval) = %v, want retry interval error", err)
	}
	command = forwardCtxTestCommand()
	mustSetFlag(t, command, "config", path)
	mustSetFlag(t, command, "retry", "false")
	ctx, err = newForwardCtx(command, "localhost", "8080")
	if err != nil {
		t.Fatalf("newForwardCtx(retry=false) error = %v", err)
	}
	if ctx.AutoReconnect == nil || *ctx.AutoReconnect {
		t.Fatalf("retry=false should disable reconnect: %#v", ctx.AutoReconnect)
	}
}

func TestForwardProxyTCPForwardsBytes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			serverErr <- err
			return
		}
		if string(buf) != "ping" {
			serverErr <- errors.New("unexpected tcp payload")
			return
		}
		_, err = conn.Write([]byte("pong"))
		serverErr <- err
	}()
	client, inbound := net.Pipe()
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	ctx := &forwardCtx{Host: "127.0.0.1", Port: port, OutputFormat: forwardOutputFormatNone, Logger: slog.Default()}
	ctx.proxyTCP(inbound)
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatalf("client Write() error = %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatalf("client Read() error = %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("client read %q, want pong", buf)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestForwardProxyUDPForwardsPackets(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer server.Close()
	_, port, err := net.SplitHostPort(server.LocalAddr().String())
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	serverErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 32)
		n, addr, err := server.ReadFrom(buf)
		if err != nil {
			serverErr <- err
			return
		}
		_, err = server.WriteTo(append([]byte("echo:"), buf[:n]...), addr)
		serverErr <- err
	}()
	inbound := newForwardUDPProxyPacketConn(forwardStubAddr("client"))
	inbound.reads <- []byte("ping")
	ctx := &forwardCtx{Host: "127.0.0.1", Port: port, OutputFormat: forwardOutputFormatNone, Logger: slog.Default()}
	ctx.proxyUDP(inbound, forwardStubAddr("client"))
	select {
	case got := <-inbound.writes:
		if string(got) != "echo:ping" {
			t.Fatalf("udp response = %q, want echo:ping", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for UDP response")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("udp server error = %v", err)
	}
}

type forwardStubAddr string

func (a forwardStubAddr) Network() string { return "stub" }
func (a forwardStubAddr) String() string  { return string(a) }

type forwardUDPProxyPacketConn struct {
	laddr       net.Addr
	reads       chan []byte
	writes      chan []byte
	releaseRead chan struct{}
	closed      chan struct{}
	releaseOnce sync.Once
	closeOnce   sync.Once
}

func newForwardUDPProxyPacketConn(laddr net.Addr) *forwardUDPProxyPacketConn {
	return &forwardUDPProxyPacketConn{
		laddr:       laddr,
		reads:       make(chan []byte, 1),
		writes:      make(chan []byte, 1),
		releaseRead: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (c *forwardUDPProxyPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data := <-c.reads:
		return copy(p, data), c.laddr, nil
	case <-c.releaseRead:
		return 0, nil, io.EOF
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *forwardUDPProxyPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	c.writes <- append([]byte(nil), p...)
	c.releaseOnce.Do(func() { close(c.releaseRead) })
	return len(p), nil
}

func (c *forwardUDPProxyPacketConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *forwardUDPProxyPacketConn) LocalAddr() net.Addr              { return c.laddr }
func (c *forwardUDPProxyPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *forwardUDPProxyPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *forwardUDPProxyPacketConn) SetWriteDeadline(time.Time) error { return nil }

func forwardCtxTestCommand() *cobra.Command {
	command := tunnelFlagsCommand()
	command.Flags().String("config", "", "")
	command.Flags().String("api-url", "", "")
	command.Flags().String("context", "", "")
	command.Flags().String("output", "", "")
	command.Flags().Bool("retry", true, "")
	command.Flags().Bool("no-retry", false, "")
	command.Flags().Int64("retry-interval", 5000, "")
	return command
}
