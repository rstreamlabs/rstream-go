// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
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

type acceptedPacketConn struct {
	conn net.PacketConn
	addr net.Addr
}

type fakePacketListener struct {
	addr     net.Addr
	accepted chan acceptedPacketConn
	closed   chan struct{}
	once     sync.Once
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
	return nil
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
	if err := packetConn.SetDeadline(time.Now()); err == nil || !strings.Contains(err.Error(), "unimplemented") {
		t.Fatalf("SetDeadline() error = %v, want unimplemented error", err)
	}
	if err := packetConn.SetReadDeadline(time.Now()); err == nil || !strings.Contains(err.Error(), "unimplemented") {
		t.Fatalf("SetReadDeadline() error = %v, want unimplemented error", err)
	}
	if err := packetConn.SetWriteDeadline(time.Now()); err == nil || !strings.Contains(err.Error(), "unimplemented") {
		t.Fatalf("SetWriteDeadline() error = %v, want unimplemented error", err)
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
}
