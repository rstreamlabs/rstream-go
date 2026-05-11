// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
)

func TestNetcatEndpointStringAndRejectedRstreamForms(t *testing.T) {
	name := "ssh"
	if got := (netcatDialTarget{Kind: netcatEndpointRstream, Address: "ssh"}).String(); got != "rstrm://ssh" {
		t.Fatalf("dial target String() = %q", got)
	}
	if got := (netcatListenTarget{Kind: netcatEndpointRstream, Name: &name}).String(); got != "rstrm://ssh" {
		t.Fatalf("listen target String() = %q", got)
	}
	if got := (netcatListenTarget{Kind: netcatEndpointRstream}).String(); got != "rstrm://" {
		t.Fatalf("anonymous listen target String() = %q", got)
	}
	for _, raw := range []string{"rstrm://user@ssh", "rstrm://ssh?x=1", "rstrm://ssh#frag", "rstrm://ssh/nested", "ftp://ssh"} {
		if _, err := parseNetcatDialTarget(raw); err == nil {
			t.Fatalf("parseNetcatDialTarget(%q) succeeded unexpectedly", raw)
		}
	}
	target, err := parseNetcatDialTarget("rstrm:///ssh")
	if err != nil || target.Address != "ssh" {
		t.Fatalf("path-only rstream target = %#v err=%v", target, err)
	}
}

func TestNewNetcatClientConfigForTCPAndDialer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	command := newTestNetcatCommand()
	mustSetFlag(t, command, "no-interactive", "true")
	cfg, err := newNetcatClientConfig(command, slog.Default(), listener.Addr().String())
	if err != nil {
		t.Fatalf("newNetcatClientConfig() error = %v", err)
	}
	if cfg.Target != listener.Addr().String() || cfg.Interactive || !cfg.HalfClose || cfg.Dial == nil {
		t.Fatalf("unexpected client config: %#v", cfg)
	}
	conn, err := cfg.Dial(t.Context())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	select {
	case serverConn := <-accepted:
		_ = serverConn.Close()
	case <-time.After(time.Second):
		t.Fatalf("server did not accept TCP netcat dial")
	}
}

func TestNewNetcatServerConfigForTCPRemoteAndExec(t *testing.T) {
	command := newTestNetcatCommand()
	mustSetFlag(t, command, "listen", "127.0.0.1:0")
	mustSetFlag(t, command, "remote", "127.0.0.1:1")
	cfg, err := newNetcatServerConfig(command, slog.Default())
	if err != nil {
		t.Fatalf("newNetcatServerConfig(remote) error = %v", err)
	}
	if cfg.Listen == nil || cfg.Upstream == nil || !cfg.DownstreamHalfClose || !cfg.UpstreamHalfClose || cfg.Exec != nil {
		t.Fatalf("unexpected remote server config: %#v", cfg)
	}
	result, err := cfg.Listen(t.Context())
	if err != nil {
		t.Fatalf("Listen factory error = %v", err)
	}
	_ = result.Listener.Close()
	if result.Display == "" || result.Generated {
		t.Fatalf("unexpected listen result: %#v", result)
	}
	command = newTestNetcatCommand()
	mustSetFlag(t, command, "listen", "127.0.0.1:0")
	mustSetFlag(t, command, "sh-exec", "printf ok")
	cfg, err = newNetcatServerConfig(command, slog.Default())
	if err != nil {
		t.Fatalf("newNetcatServerConfig(exec) error = %v", err)
	}
	if cfg.Exec == nil || cfg.Exec.Command != "printf ok" || !cfg.Exec.Shell || cfg.Upstream != nil {
		t.Fatalf("unexpected exec server config: %#v", cfg)
	}
}

func TestNetcatRstreamHelpersFailWithoutClientAndDisplayTunnel(t *testing.T) {
	t.Setenv("RSTREAM_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	if _, err := newNetcatRstreamClient(newTestNetcatCommand(), true); err == nil || !strings.Contains(err.Error(), "failed to resolve runtime") {
		t.Fatalf("expected missing runtime error, got %v", err)
	}
	dialer := newNetcatDialer(netcatDialTarget{Kind: netcatEndpointRstream, Address: "ssh"}, nil)
	if _, err := dialer(t.Context()); err == nil || !strings.Contains(err.Error(), "rstream client is required") {
		t.Fatalf("expected missing rstream client error, got %v", err)
	}
	listen := newNetcatListenerFactory(netcatListenTarget{Kind: netcatEndpointRstream, Name: rstream.StringPtr("ssh")}, nil)
	if _, err := listen(t.Context()); err == nil || !strings.Contains(err.Error(), "rstream client is required") {
		t.Fatalf("expected missing rstream listener client error, got %v", err)
	}
	display, err := netcatTunnelDisplay(rstream.TunnelProperties{Name: rstream.StringPtr("named"), ID: rstream.StringPtr("id")})
	if err != nil || display != "rstrm://named" {
		t.Fatalf("name display = %q err=%v", display, err)
	}
	display, err = netcatTunnelDisplay(rstream.TunnelProperties{ID: rstream.StringPtr("id")})
	if err != nil || display != "rstrm://id" {
		t.Fatalf("id display = %q err=%v", display, err)
	}
	if _, err := netcatTunnelDisplay(rstream.TunnelProperties{}); err == nil {
		t.Fatalf("expected missing tunnel identifier error")
	}
}

func TestNetcatCopyAndHalfCloseHelpers(t *testing.T) {
	dst := &halfCloseRecorderConn{Reader: strings.NewReader(""), Writer: io.Discard}
	src := &halfCloseRecorderConn{Reader: strings.NewReader("payload"), Writer: io.Discard}
	n, err := copyNetcatStream(dst, src, true)
	if err != nil || n != int64(len("payload")) {
		t.Fatalf("copyNetcatStream() = %d, %v", n, err)
	}
	if dst.writes.String() != "payload" || !dst.closeWriteCalled || !src.closeReadCalled {
		t.Fatalf("half-close state dst=%#v src=%#v", dst, src)
	}
	dst.closeWriteErr = errors.New("close write")
	if err := closeNetcatWrite(dst); err == nil || !strings.Contains(err.Error(), "close write") {
		t.Fatalf("expected close write error, got %v", err)
	}
	src.closeReadErr = errors.New("close read")
	if err := closeNetcatRead(src); err == nil || !strings.Contains(err.Error(), "close read") {
		t.Fatalf("expected close read error, got %v", err)
	}
	if !isNetcatClosedError(net.ErrClosed) || isNetcatClosedError(errors.New("open")) {
		t.Fatalf("closed error detection is wrong")
	}
	if firstNetcatError(nil, errors.New("first"), errors.New("second")).Error() != "first" {
		t.Fatalf("firstNetcatError did not return first non-nil error")
	}
}

type halfCloseRecorderConn struct {
	io.Reader
	io.Writer
	writes           strings.Builder
	closeWriteCalled bool
	closeReadCalled  bool
	closeWriteErr    error
	closeReadErr     error
}

func (c *halfCloseRecorderConn) Read(p []byte) (int, error) {
	return c.Reader.Read(p)
}

func (c *halfCloseRecorderConn) Write(p []byte) (int, error) {
	c.writes.Write(p)
	return len(p), nil
}

func (c *halfCloseRecorderConn) Close() error {
	return nil
}

func (c *halfCloseRecorderConn) LocalAddr() net.Addr {
	return netcatTestAddr("local")
}

func (c *halfCloseRecorderConn) RemoteAddr() net.Addr {
	return netcatTestAddr("remote")
}

func (c *halfCloseRecorderConn) SetDeadline(time.Time) error {
	return nil
}

func (c *halfCloseRecorderConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *halfCloseRecorderConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (c *halfCloseRecorderConn) CloseWrite() error {
	c.closeWriteCalled = true
	return c.closeWriteErr
}

func (c *halfCloseRecorderConn) CloseRead() error {
	c.closeReadCalled = true
	return c.closeReadErr
}

type netcatTestAddr string

func (a netcatTestAddr) Network() string { return "test" }
func (a netcatTestAddr) String() string  { return string(a) }

var _ net.Conn = (*halfCloseRecorderConn)(nil)
