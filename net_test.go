// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/sctp"
)

func TestPacketConnFromConnFramed(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	remote := stubNetAddr("right")
	pc := PacketConnFromConn(left, remote, PacketModeFramed)
	if pc.LocalAddr().String() != left.LocalAddr().String() {
		t.Fatalf("LocalAddr() = %v, want %v", pc.LocalAddr(), left.LocalAddr())
	}
	if err := pc.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if err := pc.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := pc.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- writeMessage(bufio.NewWriter(right), []byte("hello"))
	}()
	buf := make([]byte, 16)
	n, addr, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read framed packet: %v", err)
	}
	if string(buf[:n]) != "hello" || addr.String() != remote.String() {
		t.Fatalf("unexpected read: %q from %v", string(buf[:n]), addr)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	readCh := make(chan []byte, 1)
	readErrCh := make(chan error, 1)
	go func() {
		got, err := readMessage(bufio.NewReader(right))
		if err != nil {
			readErrCh <- err
			return
		}
		readCh <- got
	}()
	if n, err := pc.WriteTo([]byte("ack"), remote); err != nil || n != 3 {
		t.Fatalf("write framed packet: n=%d err=%v", n, err)
	}
	var got []byte
	select {
	case got = <-readCh:
	case err := <-readErrCh:
		t.Fatalf("read response frame: %v", err)
	case <-time.After(time.Second):
		t.Fatalf("timed out reading response frame")
	}
	if string(got) != "ack" {
		t.Fatalf("got response %q", got)
	}
	if _, err := pc.WriteTo([]byte("bad"), stubNetAddr("other")); err == nil {
		t.Fatalf("expected invalid remote address error")
	}
}

func TestFramedPacketSizeAndShortReadErrors(t *testing.T) {
	var out bytes.Buffer
	err := writeMessage(bufio.NewWriter(&out), bytes.Repeat([]byte("x"), maxFramedPacketSize+1))
	if err == nil || !strings.Contains(err.Error(), "framed packet too large") {
		t.Fatalf("writeMessage(oversize) error = %v", err)
	}
	var oversized bytes.Buffer
	if err := binary.Write(&oversized, binary.BigEndian, uint32(maxFramedPacketSize+1)); err != nil {
		t.Fatalf("binary.Write() error = %v", err)
	}
	if _, err := readMessage(bufio.NewReader(&oversized)); err == nil || !strings.Contains(err.Error(), "framed packet too large") {
		t.Fatalf("readMessage(oversize) error = %v", err)
	}
	var short bytes.Buffer
	if err := binary.Write(&short, binary.BigEndian, uint32(4)); err != nil {
		t.Fatalf("binary.Write() error = %v", err)
	}
	short.Write([]byte("xy"))
	if _, err := readMessage(bufio.NewReader(&short)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readMessage(short) error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestPacketListenerFromListenerAcceptsAndWrapsConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	packetListener := PacketListenerFromListener(listener)
	defer packetListener.Close()
	if packetListener.Addr().String() != listener.Addr().String() {
		t.Fatalf("Addr() = %v, want %v", packetListener.Addr(), listener.Addr())
	}
	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer clientConn.Close()
	packetConn, remote, err := packetListener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	defer packetConn.Close()
	if remote == nil {
		t.Fatalf("Accept() remote address = nil")
	}
	clientReader := bufio.NewReader(clientConn)
	clientWriter := bufio.NewWriter(clientConn)
	if err := writeMessage(clientWriter, []byte("client-packet")); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	buf := make([]byte, 32)
	n, addr, err := packetConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "client-packet" || addr.String() != remote.String() {
		t.Fatalf("ReadFrom() = %q from %v, want client-packet from %v", buf[:n], addr, remote)
	}
	if n, err := packetConn.WriteTo([]byte("server-packet"), remote); err != nil || n != len("server-packet") {
		t.Fatalf("WriteTo() = %d, %v; want %d, nil", n, err, len("server-packet"))
	}
	got, err := readMessage(clientReader)
	if err != nil {
		t.Fatalf("read server frame: %v", err)
	}
	if string(got) != "server-packet" {
		t.Fatalf("server frame = %q, want server-packet", got)
	}
	if err := packetListener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestConnFromPacketConnWritesToFixedRemote(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(server) error = %v", err)
	}
	defer server.Close()
	client, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(client) error = %v", err)
	}
	defer client.Close()
	conn := ConnFromPacketConn(client, server.LocalAddr())
	defer conn.Close()
	if conn.LocalAddr().String() != client.LocalAddr().String() {
		t.Fatalf("LocalAddr() = %v, want %v", conn.LocalAddr(), client.LocalAddr())
	}
	if conn.RemoteAddr().String() != server.LocalAddr().String() {
		t.Fatalf("RemoteAddr() = %v, want %v", conn.RemoteAddr(), server.LocalAddr())
	}
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		n, raddr, err := server.ReadFrom(buf)
		if err != nil {
			errCh <- err
			return
		}
		if string(buf[:n]) != "hello" {
			errCh <- errors.New("unexpected request")
			return
		}
		_, err = server.WriteTo([]byte("ack"), raddr)
		errCh <- err
	}()
	if n, err := conn.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("Write() = %d, %v; want 5, nil", n, err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(buf[:n]) != "ack" {
		t.Fatalf("Read() = %q, want ack", buf[:n])
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := conn.Write([]byte("closed")); err == nil {
		t.Fatalf("Write() after Close() error = nil")
	}
}

func TestConnFromPacketConnLearnsRemoteAddress(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(server) error = %v", err)
	}
	defer server.Close()
	client, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(client) error = %v", err)
	}
	defer client.Close()
	conn := ConnFromPacketConn(server, nil)
	defer conn.Close()
	if _, err := conn.Write([]byte("before-read")); err == nil || !strings.Contains(err.Error(), "remote address is not set") {
		t.Fatalf("Write() before remote error = %v", err)
	}
	if _, err := client.WriteTo([]byte("hello"), server.LocalAddr()); err != nil {
		t.Fatalf("client WriteTo() error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("Read() = %q, want hello", buf[:n])
	}
	if conn.RemoteAddr().String() != client.LocalAddr().String() {
		t.Fatalf("RemoteAddr() = %v, want %v", conn.RemoteAddr(), client.LocalAddr())
	}
	if _, err := conn.Write([]byte("ack")); err != nil {
		t.Fatalf("Write() after remote error = %v", err)
	}
}

func TestConnFromPacketConnSkipsUnexpectedRemote(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(server) error = %v", err)
	}
	defer server.Close()
	expected, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(expected) error = %v", err)
	}
	defer expected.Close()
	noise, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(noise) error = %v", err)
	}
	defer noise.Close()
	conn := ConnFromPacketConn(server, expected.LocalAddr())
	defer conn.Close()
	if _, err := noise.WriteTo([]byte("noise"), server.LocalAddr()); err != nil {
		t.Fatalf("noise WriteTo() error = %v", err)
	}
	if _, err := expected.WriteTo([]byte("hello"), server.LocalAddr()); err != nil {
		t.Fatalf("expected WriteTo() error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("Read() = %q, want hello", buf[:n])
	}
}

func TestConnFromPacketConnCarriesSCTP(t *testing.T) {
	serverPacketConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(server) error = %v", err)
	}
	defer serverPacketConn.Close()
	clientPacketConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(client) error = %v", err)
	}
	defer clientPacketConn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if err := serverPacketConn.SetDeadline(deadline); err != nil {
		t.Fatalf("server SetDeadline() error = %v", err)
	}
	if err := clientPacketConn.SetDeadline(deadline); err != nil {
		t.Fatalf("client SetDeadline() error = %v", err)
	}
	serverConn := ConnFromPacketConn(serverPacketConn, nil)
	clientConn := ConnFromPacketConn(clientPacketConn, serverPacketConn.LocalAddr())
	errCh := make(chan error, 1)
	releaseServer := make(chan struct{})
	go func() {
		assoc, err := sctp.ServerWithOptions(sctp.WithNetConn(serverConn))
		if err != nil {
			errCh <- err
			return
		}
		defer assoc.Close()
		stream, err := assoc.AcceptStream()
		if err != nil {
			errCh <- err
			return
		}
		defer stream.Close()
		buf := make([]byte, 32)
		n, err := stream.Read(buf)
		if err != nil {
			errCh <- err
			return
		}
		_, err = stream.WriteSCTP(buf[:n], sctp.PayloadTypeWebRTCString)
		if err == nil {
			<-releaseServer
		}
		errCh <- err
	}()
	assoc, err := sctp.ClientWithOptions(sctp.WithNetConn(clientConn))
	if err != nil {
		t.Fatalf("sctp.Client() error = %v", err)
	}
	defer assoc.Close()
	stream, err := assoc.OpenStream(0, sctp.PayloadTypeWebRTCString)
	if err != nil {
		t.Fatalf("OpenStream() error = %v", err)
	}
	defer stream.Close()
	if err := stream.SetDeadline(deadline); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if _, err := stream.Write([]byte("sctp-ping")); err != nil {
		t.Fatalf("stream Write() error = %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server SCTP write error = %v", err)
		}
		t.Fatalf("server SCTP exited before client read")
	default:
	}
	readCh := make(chan []byte, 1)
	readErrCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 32)
		n, err := stream.Read(buf)
		if err != nil {
			readErrCh <- err
			return
		}
		readCh <- append([]byte(nil), buf[:n]...)
	}()
	select {
	case got := <-readCh:
		if string(got) != "sctp-ping" {
			t.Fatalf("stream Read() = %q, want sctp-ping", got)
		}
	case err := <-readErrCh:
		t.Fatalf("stream Read() error = %v", err)
	case err := <-errCh:
		t.Fatalf("server SCTP error before echo = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for SCTP echo")
	}
	close(releaseServer)
	if err := <-errCh; err != nil {
		t.Fatalf("server SCTP error = %v", err)
	}
}

type stubNetAddr string

func (a stubNetAddr) Network() string { return "stub" }
func (a stubNetAddr) String() string  { return string(a) }

type fakePacketConn struct {
	laddr     net.Addr
	reads     chan []byte
	writes    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakePacketConn(laddr net.Addr) *fakePacketConn {
	return &fakePacketConn{
		laddr:  laddr,
		reads:  make(chan []byte, 4),
		writes: make(chan []byte, 4),
		closed: make(chan struct{}),
	}
}

func (c *fakePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data := <-c.reads:
		return copy(p, data), c.laddr, nil
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *fakePacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	c.writes <- append([]byte(nil), p...)
	return len(p), nil
}

func (c *fakePacketConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *fakePacketConn) LocalAddr() net.Addr              { return c.laddr }
func (c *fakePacketConn) SetDeadline(time.Time) error      { return nil }
func (c *fakePacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakePacketConn) SetWriteDeadline(time.Time) error { return nil }

type blockingPacketConn struct {
	*fakePacketConn
	writeStarted chan struct{}
}

func newBlockingPacketConn(laddr net.Addr) *blockingPacketConn {
	return &blockingPacketConn{fakePacketConn: newFakePacketConn(laddr), writeStarted: make(chan struct{})}
}

func (c *blockingPacketConn) WriteTo([]byte, net.Addr) (int, error) {
	select {
	case <-c.writeStarted:
	default:
		close(c.writeStarted)
	}
	<-c.closed
	return 0, net.ErrClosed
}

type acceptedPacketConn struct {
	conn net.PacketConn
	addr net.Addr
}

type fakePacketListener struct {
	addr     net.Addr
	accepted chan acceptedPacketConn
	closed   chan struct{}
	once     sync.Once
	closeErr error
}

func newFakePacketListener(addr net.Addr) *fakePacketListener {
	return &fakePacketListener{
		addr:     addr,
		accepted: make(chan acceptedPacketConn, 4),
		closed:   make(chan struct{}),
	}
}

func (l *fakePacketListener) Accept() (net.PacketConn, net.Addr, error) {
	select {
	case accepted := <-l.accepted:
		return accepted.conn, accepted.addr, nil
	case <-l.closed:
		return nil, nil, net.ErrClosed
	}
}

func (l *fakePacketListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return l.closeErr
}

func (l *fakePacketListener) Addr() net.Addr { return l.addr }

func TestPacketConnFromPacketListenerRoutesPackets(t *testing.T) {
	remote := stubNetAddr("remote")
	inner := newFakePacketConn(stubNetAddr("inner"))
	listener := newFakePacketListener(stubNetAddr("listener"))
	packetConn := PacketConnFromPacketListener(listener)
	defer packetConn.Close()
	if packetConn.LocalAddr() != listener.addr {
		t.Fatalf("LocalAddr() = %v, want %v", packetConn.LocalAddr(), listener.addr)
	}
	if err := packetConn.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if _, _, err := packetConn.ReadFrom(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("ReadFrom() with expired deadline error = %v, want deadline exceeded", err)
	}
	if err := packetConn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("SetReadDeadline() clear error = %v", err)
	}
	if err := packetConn.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
	if _, err := packetConn.WriteTo([]byte("expired"), remote); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("WriteTo() with expired deadline error = %v, want deadline exceeded", err)
	}
	if err := packetConn.SetDeadline(time.Time{}); err != nil {
		t.Fatalf("SetDeadline() clear error = %v", err)
	}
	listener.accepted <- acceptedPacketConn{conn: inner, addr: remote}
	inner.reads <- []byte("hello")
	buf := make([]byte, 16)
	n, addr, err := packetConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if string(buf[:n]) != "hello" || addr != remote {
		t.Fatalf("ReadFrom() = %q from %v, want hello from %v", buf[:n], addr, remote)
	}
	if n, err := packetConn.WriteTo([]byte("ack"), remote); err != nil || n != 3 {
		t.Fatalf("WriteTo() = %d, %v; want 3, nil", n, err)
	}
	select {
	case got := <-inner.writes:
		if string(got) != "ack" {
			t.Fatalf("routed write = %q, want ack", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for routed write")
	}
	if _, err := packetConn.WriteTo([]byte("lost"), stubNetAddr("missing")); err == nil {
		t.Fatalf("expected missing remote error")
	}
	if err := packetConn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := packetConn.WriteTo([]byte("closed"), remote); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("WriteTo() after close error = %v, want net.ErrClosed", err)
	}
	if err := packetConn.SetDeadline(time.Time{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetDeadline() after close error = %v, want net.ErrClosed", err)
	}
}

func TestPacketConnFromPacketListenerPendingReadTracksDeadline(t *testing.T) {
	listener := newFakePacketListener(stubNetAddr("listener"))
	packetConn := PacketConnFromPacketListener(listener)
	defer packetConn.Close()
	result := make(chan error, 1)
	go func() {
		_, _, err := packetConn.ReadFrom(make([]byte, 1))
		result <- err
	}()
	time.Sleep(10 * time.Millisecond)
	if err := packetConn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("ReadFrom() error = %v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadFrom() did not observe the new deadline")
	}
}

func TestPacketConnFromPacketListenerPropagatesListenerClose(t *testing.T) {
	remote := stubNetAddr("remote")
	inner := newFakePacketConn(stubNetAddr("inner"))
	listener := newFakePacketListener(stubNetAddr("listener"))
	packetConn := PacketConnFromPacketListener(listener)
	listener.accepted <- acceptedPacketConn{conn: inner, addr: remote}
	inner.reads <- []byte("ready")
	buf := make([]byte, 16)
	if _, _, err := packetConn.ReadFrom(buf); err != nil {
		t.Fatalf("initial ReadFrom() error = %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("listener Close() error = %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, _, err := packetConn.ReadFrom(buf)
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("ReadFrom() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("ReadFrom() remained blocked after listener close")
	}
	select {
	case <-inner.closed:
	case <-time.After(time.Second):
		t.Fatalf("accepted connection remained open after listener close")
	}
}

func TestPacketConnFromPacketListenerCloseUnblocksRead(t *testing.T) {
	listener := newFakePacketListener(stubNetAddr("listener"))
	packetConn := PacketConnFromPacketListener(listener)
	buf := make([]byte, 16)
	result := make(chan error, 1)
	go func() {
		_, _, err := packetConn.ReadFrom(buf)
		result <- err
	}()
	if err := packetConn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("ReadFrom() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("ReadFrom() remained blocked after close")
	}
}

func TestPacketConnFromPacketListenerConcurrentCloseJoinsReaders(t *testing.T) {
	remote := stubNetAddr("remote")
	inner := newFakePacketConn(stubNetAddr("inner"))
	closeErr := errors.New("listener close failed")
	listener := newFakePacketListener(stubNetAddr("listener"))
	listener.closeErr = closeErr
	packetConn := PacketConnFromPacketListener(listener)
	listener.accepted <- acceptedPacketConn{conn: inner, addr: remote}
	inner.reads <- []byte("ready")
	if _, _, err := packetConn.ReadFrom(make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	wrapper := packetConn.(*packetListenerWrapper)
	wrapper.mu.Lock()
	entry := wrapper.conns[packetAddrKey(remote)]
	wrapper.mu.Unlock()
	if entry == nil || !entry.active.Load() {
		t.Fatal("accepted packet reader did not become active")
	}
	const callers = 64
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- packetConn.Close() }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, closeErr) {
			t.Fatalf("Close() error = %v, want %v", err, closeErr)
		}
	}
	select {
	case <-wrapper.shutdownDone:
	default:
		t.Fatal("Close() returned before listener workers stopped")
	}
	if entry.active.Load() {
		t.Fatal("accepted packet reader remained active after Close()")
	}
}

func TestPacketConnFromPacketListenerCloseInterruptsBlockedWrite(t *testing.T) {
	remote := stubNetAddr("remote")
	inner := newBlockingPacketConn(stubNetAddr("inner"))
	listener := newFakePacketListener(stubNetAddr("listener"))
	packetConn := PacketConnFromPacketListener(listener)
	listener.accepted <- acceptedPacketConn{conn: inner, addr: remote}
	inner.reads <- []byte("ready")
	buf := make([]byte, 16)
	if _, _, err := packetConn.ReadFrom(buf); err != nil {
		t.Fatalf("initial ReadFrom() error = %v", err)
	}
	writeErr := make(chan error, 1)
	go func() {
		_, err := packetConn.WriteTo([]byte("blocked"), remote)
		writeErr <- err
	}()
	select {
	case <-inner.writeStarted:
	case <-time.After(time.Second):
		t.Fatal("WriteTo() did not start")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- packetConn.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() blocked behind WriteTo()")
	}
	select {
	case err := <-writeErr:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("WriteTo() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WriteTo() remained blocked after Close()")
	}
}

func TestPacketConnFromPacketListenerReplacesSameAddressAtomically(t *testing.T) {
	remote := stubNetAddr("remote")
	first := newFakePacketConn(stubNetAddr("first"))
	second := newFakePacketConn(stubNetAddr("second"))
	listener := newFakePacketListener(stubNetAddr("listener"))
	packetConn := PacketConnFromPacketListener(listener)
	defer packetConn.Close()
	listener.accepted <- acceptedPacketConn{conn: first, addr: remote}
	first.reads <- []byte("first")
	buf := make([]byte, 16)
	if _, _, err := packetConn.ReadFrom(buf); err != nil {
		t.Fatalf("first ReadFrom() error = %v", err)
	}
	listener.accepted <- acceptedPacketConn{conn: second, addr: stubNetAddr("remote")}
	second.reads <- []byte("second")
	if _, _, err := packetConn.ReadFrom(buf); err != nil {
		t.Fatalf("second ReadFrom() error = %v", err)
	}
	select {
	case <-first.closed:
	case <-time.After(time.Second):
		t.Fatal("replaced connection remained open")
	}
	if n, err := packetConn.WriteTo([]byte("current"), remote); err != nil || n != len("current") {
		t.Fatalf("WriteTo() = %d, %v; want %d, nil", n, err, len("current"))
	}
	select {
	case got := <-second.writes:
		if string(got) != "current" {
			t.Fatalf("replacement write = %q, want current", got)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not receive WriteTo()")
	}
}

func TestPacketConnFromPacketListenerDropsQueuedPacketsFromReplacedConnection(t *testing.T) {
	remote := stubNetAddr("remote")
	first := newFakePacketConn(stubNetAddr("first"))
	second := newFakePacketConn(stubNetAddr("second"))
	listener := newFakePacketListener(stubNetAddr("listener"))
	packetConn := PacketConnFromPacketListener(listener)
	defer packetConn.Close()
	listener.accepted <- acceptedPacketConn{conn: first, addr: remote}
	first.reads <- []byte("stale")
	select {
	case <-first.closed:
		t.Fatal("first connection closed before replacement")
	case <-time.After(20 * time.Millisecond):
	}
	listener.accepted <- acceptedPacketConn{conn: second, addr: stubNetAddr("remote")}
	select {
	case <-first.closed:
	case <-time.After(time.Second):
		t.Fatal("replaced connection remained open")
	}
	second.reads <- []byte("current")
	buf := make([]byte, 16)
	if err := packetConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	n, _, err := packetConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if got := string(buf[:n]); got != "current" {
		t.Fatalf("ReadFrom() = %q, want current packet from replacement", got)
	}
}
