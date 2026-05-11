// See LICENSE file in the project root for license information.

package runengine

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestRunnerStartValidation(t *testing.T) {
	runner := New()
	_, err := runner.Start(t.Context(), runmodel.DesiredTunnel{})
	if err == nil || !strings.Contains(err.Error(), "tunnel name is required") {
		t.Fatalf("Start() missing name error = %v", err)
	}
	_, err = runner.Start(t.Context(), runmodel.DesiredTunnel{Name: "web"})
	if err == nil || !strings.Contains(err.Error(), "engine is required") {
		t.Fatalf("Start() missing engine error = %v", err)
	}
	_, err = runner.Start(t.Context(), runmodel.DesiredTunnel{Name: "web", Context: runmodel.ResolvedContext{Engine: "engine.example.com:443"}})
	if err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("Start() missing token error = %v", err)
	}
	var nilRunner *Runner
	runHandle, err := nilRunner.Start(t.Context(), runmodel.DesiredTunnel{})
	if err != nil || runHandle == nil {
		t.Fatalf("nil runner Start() = %v, %v", runHandle, err)
	}
	if err := runHandle.Stop(); err != nil {
		t.Fatalf("nil runner handle Stop() error = %v", err)
	}
	var nilHandle *handle
	if err := nilHandle.Stop(); err != nil {
		t.Fatalf("nil handle Stop() error = %v", err)
	}
	configured := New(WithRetry(2*time.Millisecond, 8*time.Millisecond))
	if configured.retryInitial != 2*time.Millisecond || configured.retryMax != 8*time.Millisecond {
		t.Fatalf("WithRetry not applied: %#v", configured)
	}
}

func TestServeWithCtxCancellationAndServeError(t *testing.T) {
	runner := New()
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	closed := make(chan struct{})
	resultCh := make(chan error, 1)
	go func() {
		resultCh <- runner.serveWithCtx(ctx, func() error {
			close(closed)
			return nil
		}, func() error {
			close(started)
			<-closed
			return errors.New("serve returned after close")
		})
	}()
	<-started
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("serveWithCtx(cancel) = %v, want context.Canceled", err)
	}
	sentinel := errors.New("serve failed")
	err := runner.serveWithCtx(t.Context(), func() error {
		t.Fatalf("closeFn should not run when serve exits first")
		return nil
	}, func() error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("serveWithCtx(error) = %v, want sentinel", err)
	}
}

func TestProxyTCPForwardsBytes(t *testing.T) {
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
	go New(WithLogger(slog.Default())).proxyTCP(inbound, runmodel.ForwardTarget{Host: "127.0.0.1", Port: port}, slog.Default())
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

func TestProxyUDPForwardsPackets(t *testing.T) {
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
	inbound := newUDPProxyPacketConn(stubAddr("client"))
	inbound.reads <- []byte("ping")
	done := make(chan struct{})
	go func() {
		New().proxyUDP(inbound, stubAddr("client"), runmodel.ForwardTarget{Host: "127.0.0.1", Port: port}, slog.Default())
		close(done)
	}()
	select {
	case got := <-inbound.writes:
		if string(got) != "echo:ping" {
			t.Fatalf("udp response = %q, want echo:ping", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for UDP response")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("proxyUDP did not return")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("udp server error = %v", err)
	}
}

func TestServeTCPAndUDPReturnAcceptErrors(t *testing.T) {
	runner := New()
	tcpErr := errors.New("tcp closed")
	tcpListener := &errorListener{err: tcpErr}
	if err := runner.serveTCP(tcpListener, runmodel.ForwardTarget{Host: "127.0.0.1", Port: "1"}, slog.Default()); !errors.Is(err, tcpErr) {
		t.Fatalf("serveTCP() = %v, want %v", err, tcpErr)
	}
	udpErr := errors.New("udp closed")
	packetListener := &errorPacketListener{err: udpErr}
	if err := runner.serveUDP(packetListener, runmodel.ForwardTarget{Host: "127.0.0.1", Port: "1"}, slog.Default()); !errors.Is(err, udpErr) {
		t.Fatalf("serveUDP() = %v, want %v", err, udpErr)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runner.run(ctx, runmodel.DesiredTunnel{Name: "web", Forward: runmodel.ForwardTarget{Host: "127.0.0.1", Port: "1"}})
}

func TestRunnerRunOnceCreatesTunnelAndClosesOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tunnelReady := make(chan struct{})
	dialer := newRunEnginePipeDialer(func(conn net.Conn) error {
		return serveRunEngineTunnelLifecycle(conn, tunnelReady)
	})
	desired := runmodel.DesiredTunnel{
		Name:    "web",
		Forward: runmodel.ForwardTarget{Host: "127.0.0.1", Port: "1"},
		Context: runmodel.ResolvedContext{
			Engine:    "engine.example.com:443",
			Token:     "token",
			Transport: dialer,
		},
		Props: rstream.TunnelProperties{Type: rstream.TunnelTypePtr(rstream.TunnelTypeBytestream)},
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- New(WithLogger(slog.Default())).runOnce(ctx, desired, slog.Default())
	}()
	select {
	case <-tunnelReady:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for tunnel creation")
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runOnce() error = %v, want context.Canceled", err)
	}
	dialer.wait(t)
}

func TestBackoffAndStringHelpers(t *testing.T) {
	backoff := newBackoff(10*time.Millisecond, 25*time.Millisecond)
	values := []time.Duration{backoff.Next(), backoff.Next(), backoff.Next(), backoff.Next()}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond, 25 * time.Millisecond}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("backoff[%d] = %s, want %s", i, values[i], want[i])
		}
	}
	defaulted := newBackoff(0, 0)
	if got := defaulted.Next(); got != time.Second {
		t.Fatalf("default backoff first value = %s, want 1s", got)
	}
	value := "id"
	if str(nil) != "" || str(&value) != "id" {
		t.Fatalf("str helper returned unexpected values")
	}
}

type stubAddr string

func (a stubAddr) Network() string { return "stub" }
func (a stubAddr) String() string  { return string(a) }

type errorListener struct {
	err error
}

func (l *errorListener) Accept() (net.Conn, error) { return nil, l.err }
func (l *errorListener) Close() error              { return nil }
func (l *errorListener) Addr() net.Addr            { return stubAddr("listener") }

type errorPacketListener struct {
	err error
}

func (l *errorPacketListener) Accept() (net.PacketConn, net.Addr, error) {
	return nil, nil, l.err
}

func (l *errorPacketListener) Close() error   { return nil }
func (l *errorPacketListener) Addr() net.Addr { return stubAddr("packet-listener") }

type udpProxyPacketConn struct {
	laddr       net.Addr
	reads       chan []byte
	writes      chan []byte
	releaseRead chan struct{}
	closed      chan struct{}
	releaseOnce sync.Once
	closeOnce   sync.Once
}

func newUDPProxyPacketConn(laddr net.Addr) *udpProxyPacketConn {
	return &udpProxyPacketConn{
		laddr:       laddr,
		reads:       make(chan []byte, 1),
		writes:      make(chan []byte, 1),
		releaseRead: make(chan struct{}),
		closed:      make(chan struct{}),
	}
}

func (c *udpProxyPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data := <-c.reads:
		return copy(p, data), c.laddr, nil
	case <-c.releaseRead:
		return 0, nil, io.EOF
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *udpProxyPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	c.writes <- append([]byte(nil), p...)
	c.releaseOnce.Do(func() { close(c.releaseRead) })
	return len(p), nil
}

func (c *udpProxyPacketConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *udpProxyPacketConn) LocalAddr() net.Addr              { return c.laddr }
func (c *udpProxyPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *udpProxyPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *udpProxyPacketConn) SetWriteDeadline(time.Time) error { return nil }

type runEnginePipeDialer struct {
	serve func(net.Conn) error
	errCh chan error
}

func newRunEnginePipeDialer(serve func(net.Conn) error) *runEnginePipeDialer {
	return &runEnginePipeDialer{serve: serve, errCh: make(chan error, 1)}
}

func (d *runEnginePipeDialer) Dial(_ context.Context, _ string, _ *tls.Config) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		d.errCh <- d.serve(server)
	}()
	return client, nil
}

func (d *runEnginePipeDialer) wait(t *testing.T) {
	t.Helper()
	select {
	case err := <-d.errCh:
		if err != nil {
			t.Fatalf("engine server error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for engine server")
	}
}

func serveRunEngineTunnelLifecycle(conn net.Conn, tunnelReady chan<- struct{}) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	msg, err := readRunEnginePBMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetOpenControlChannelReq() == nil {
		return errors.New("expected OpenControlChannelReq")
	}
	if err := writeRunEnginePBMessage(writer, &pb.Message{Payload: &pb.Message_OpenControlChannelRsp{OpenControlChannelRsp: &pb.OpenControlChannelRsp{
		Payload: &pb.OpenControlChannelRsp_Ok_{Ok: &pb.OpenControlChannelRsp_Ok{
			ClientId: "client-1",
			ServerDetails: &pb.ServerDetails{
				Agent: wrapperspb.String("engine"),
			},
		}},
	}}}); err != nil {
		return err
	}
	msg, err = readRunEnginePBMessage(reader)
	if err != nil {
		return err
	}
	openTunnelReq := msg.GetOpenTunnelReq()
	if openTunnelReq == nil {
		return errors.New("expected OpenTunnelReq")
	}
	if err := writeRunEnginePBMessage(writer, &pb.Message{Payload: &pb.Message_OpenTunnelRsp{OpenTunnelRsp: &pb.OpenTunnelRsp{
		RequestId: openTunnelReq.RequestId,
		Payload: &pb.OpenTunnelRsp_TunnelProperties{TunnelProperties: &pb.TunnelProperties{
			Id:   wrapperspb.String("tun-1"),
			Name: wrapperspb.String("web"),
			Type: wrapperspb.String(string(rstream.TunnelTypeBytestream)),
		}},
	}}}); err != nil {
		return err
	}
	close(tunnelReady)
	msg, err = readRunEnginePBMessage(reader)
	if err != nil {
		return err
	}
	closeTunnelReq := msg.GetCloseTunnelReq()
	if closeTunnelReq == nil || closeTunnelReq.TunnelId != "tun-1" {
		return errors.New("expected CloseTunnelReq for tun-1")
	}
	if err := writeRunEnginePBMessage(writer, &pb.Message{Payload: &pb.Message_CloseTunnelRsp{CloseTunnelRsp: &pb.CloseTunnelRsp{TunnelId: "tun-1"}}}); err != nil {
		return err
	}
	msg, err = readRunEnginePBMessage(reader)
	if err != nil {
		return err
	}
	if msg.GetCloseControlChannelReq() == nil {
		return errors.New("expected CloseControlChannelReq")
	}
	return writeRunEnginePBMessage(writer, &pb.Message{Payload: &pb.Message_CloseControlChannelRsp{CloseControlChannelRsp: &pb.CloseControlChannelRsp{}}})
}

func readRunEnginePBMessage(r *bufio.Reader) (*pb.Message, error) {
	lengthBytes := make([]byte, 4)
	if _, err := io.ReadFull(r, lengthBytes); err != nil {
		return nil, err
	}
	msgBytes := make([]byte, binary.BigEndian.Uint32(lengthBytes))
	if _, err := io.ReadFull(r, msgBytes); err != nil {
		return nil, err
	}
	msg := &pb.Message{}
	if err := proto.Unmarshal(msgBytes, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func writeRunEnginePBMessage(w *bufio.Writer, msg *pb.Message) error {
	msgBytes, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, uint32(len(msgBytes)))
	if _, err := w.Write(lengthBytes); err != nil {
		return err
	}
	if _, err := w.Write(msgBytes); err != nil {
		return err
	}
	return w.Flush()
}
