// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type PacketListener interface {
	Accept() (net.PacketConn, net.Addr, error)
	Close() error
	Addr() net.Addr
}

type packet struct {
	data []byte
	adrr net.Addr
}

type listenerWrapper struct {
	inner net.Listener
}

type PacketMode int

const (
	PacketModeFramed PacketMode = iota
	PacketModeRaw
	maxFramedPacketSize = 65535
)

type connWrapper struct {
	inner net.Conn
	mode  PacketMode
	w     *bufio.Writer
	wmu   sync.Mutex
	r     *bufio.Reader
	raddr net.Addr
}

type packetListenerWrapper struct {
	mu     sync.Mutex
	closed bool
	inner  PacketListener
	conns  map[net.Addr]net.PacketConn
	pkts   chan packet
}

func PacketListenerFromListener(l net.Listener) PacketListener {
	return &listenerWrapper{inner: l}
}

func (l *listenerWrapper) Accept() (net.PacketConn, net.Addr, error) {
	conn, err := l.inner.Accept()
	if err != nil {
		return nil, nil, err
	}
	raddr := conn.RemoteAddr()
	return PacketConnFromConn(conn, raddr, PacketModeFramed), raddr, nil
}

func (l *listenerWrapper) Close() error {
	return l.inner.Close()
}

func (l *listenerWrapper) Addr() net.Addr {
	return l.inner.Addr()
}

func PacketConnFromConn(c net.Conn, raddr net.Addr, mode PacketMode) net.PacketConn {
	if raddr == nil {
		raddr = c.RemoteAddr()
	}
	pc := &connWrapper{inner: c, mode: mode, raddr: raddr}
	if mode == PacketModeFramed {
		pc.w = bufio.NewWriter(c)
		pc.r = bufio.NewReader(c)
	}
	return pc
}

func PacketConnFromDTLSConn(c *dtls.Conn) net.PacketConn {
	return PacketConnFromConn(c, nil, PacketModeRaw)
}

func (c *connWrapper) ReadFrom(p []byte) (int, net.Addr, error) {
	if c.mode == PacketModeFramed {
		msg, err := readMessage(c.r)
		if err != nil {
			return 0, nil, err
		}
		n := copy(p, msg)
		return n, c.raddr, nil
	}
	n, err := c.inner.Read(p)
	return n, c.raddr, err
}

func (c *connWrapper) WriteTo(p []byte, addr net.Addr) (int, error) {
	if addr == nil || addr.String() != c.raddr.String() {
		return 0, fmt.Errorf("invalid remote address %v; expected %v", addr, c.raddr)
	}
	if c.mode == PacketModeFramed {
		c.wmu.Lock()
		defer c.wmu.Unlock()
		if err := writeMessage(c.w, p); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return c.inner.Write(p)
}

func (c *connWrapper) Close() error {
	return c.inner.Close()
}

func (c *connWrapper) LocalAddr() net.Addr {
	return c.inner.LocalAddr()
}

func (c *connWrapper) SetDeadline(t time.Time) error {
	return c.inner.SetDeadline(t)
}

func (c *connWrapper) SetReadDeadline(t time.Time) error {
	return c.inner.SetReadDeadline(t)
}

func (c *connWrapper) SetWriteDeadline(t time.Time) error {
	return c.inner.SetWriteDeadline(t)
}

func PacketConnFromPacketListener(l PacketListener) net.PacketConn {
	pl := &packetListenerWrapper{
		inner: l,
		conns: make(map[net.Addr]net.PacketConn),
		pkts:  make(chan packet, 100),
	}
	go pl.accept()
	return pl
}

func (pl *packetListenerWrapper) accept() {
	for {
		conn, raddr, err := pl.inner.Accept()
		pl.mu.Lock()
		if err != nil || pl.closed {
			pl.mu.Unlock()
			break
		} else {
			pl.conns[raddr] = conn
			pl.mu.Unlock()
			go pl.read(conn, raddr)
		}
	}
}

func (pl *packetListenerWrapper) read(conn net.PacketConn, raddr net.Addr) {
	defer func() {
		conn.Close()
		pl.mu.Lock()
		defer pl.mu.Unlock()
		if pl.closed {
			return
		}
		delete(pl.conns, raddr)
	}()
	for {
		buf := make([]byte, 65535)
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		pkt := packet{data: buf[:n], adrr: raddr}
		select {
		case pl.pkts <- pkt:
		default:
			continue
		}
	}
}

func (pl *packetListenerWrapper) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	pkt, ok := <-pl.pkts
	if !ok {
		return 0, nil, net.ErrClosed
	}
	n = copy(p, pkt.data)
	return n, pkt.adrr, nil
}

func (pl *packetListenerWrapper) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.closed {
		return 0, net.ErrClosed
	}
	conn, ok := pl.conns[addr]
	if !ok {
		return 0, fmt.Errorf("no connection to %v", addr)
	}
	return conn.WriteTo(p, addr)
}

func (pl *packetListenerWrapper) Close() error {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.closed {
		return nil
	}
	pl.closed = true
	for _, conn := range pl.conns {
		conn.Close()
	}
	pl.conns = nil
	return pl.inner.Close()
}

func (pl *packetListenerWrapper) LocalAddr() net.Addr {
	return pl.inner.Addr()
}

func (pl *packetListenerWrapper) SetDeadline(t time.Time) error {
	return errors.New("unimplemented function")
}

func (pl *packetListenerWrapper) SetReadDeadline(t time.Time) error {
	return errors.New("unimplemented function")
}

func (pl *packetListenerWrapper) SetWriteDeadline(t time.Time) error {
	return errors.New("unimplemented function")
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	lengthBytes := make([]byte, 4)
	if _, err := io.ReadFull(r, lengthBytes); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lengthBytes)
	if length > maxFramedPacketSize {
		return nil, fmt.Errorf("framed packet too large: %d bytes", length)
	}
	msgBytes := make([]byte, length)
	if _, err := io.ReadFull(r, msgBytes); err != nil {
		return nil, err
	}
	return msgBytes, nil
}

func writeMessage(w *bufio.Writer, msgBytes []byte) error {
	if len(msgBytes) > maxFramedPacketSize {
		return fmt.Errorf("framed packet too large: %d bytes", len(msgBytes))
	}
	length := uint32(len(msgBytes))
	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, length)
	if _, err := w.Write(lengthBytes); err != nil {
		return err
	}
	if _, err := w.Write(msgBytes); err != nil {
		return err
	}
	return w.Flush()
}

func logProto(dir string, m proto.Message) {
	if Channel != "dev" {
		return
	}
	b, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(m)
	if err != nil {
		slog.With("component", "net").Debug("proto marshal error", slog.String("error", err.Error()))
	} else {
		slog.With("component", "net").Debug(
			dir,
			slog.String("type", string(m.ProtoReflect().Descriptor().FullName())),
			slog.Any("message", rawJSON(b)),
		)
	}
}

func readPbMessage(r *bufio.Reader) (*pb.Message, error) {
	msgBytes, err := readMessage(r)
	if err != nil {
		return nil, err
	}
	msg := &pb.Message{}
	if err := proto.Unmarshal(msgBytes, msg); err != nil {
		return nil, err
	}
	logProto("received message", msg)
	return msg, nil
}

func writePbMessage(w *bufio.Writer, msg *pb.Message) error {
	logProto("sending message", msg)
	msgBytes, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	return writeMessage(w, msgBytes)
}
