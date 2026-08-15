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
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/proto"
)

type PacketListener interface {
	Accept() (net.PacketConn, net.Addr, error)
	Close() error
	Addr() net.Addr
}

type packet struct {
	data   []byte
	addr   net.Addr
	source *packetListenerConn
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

type packetConnWrapper struct {
	inner net.PacketConn
	mu    sync.RWMutex
	raddr net.Addr
}

type packetListenerWrapper struct {
	mu               sync.Mutex
	closed           bool
	admissionStopped bool
	closeErr         error
	shutdownErr      error
	shutdownOnce     sync.Once
	innerClose       sync.Once
	doneOnce         sync.Once
	admissionOnce    sync.Once
	admissionDone    chan struct{}
	shutdownDone     chan struct{}
	readers          sync.WaitGroup
	inner            PacketListener
	conns            map[string]*packetListenerConn
	pkts             chan packet
	done             chan struct{}
	readDeadline     *packetDeadline
	writeDeadline    *packetDeadline
}

type packetListenerConn struct {
	conn   net.PacketConn
	addr   net.Addr
	active atomic.Bool
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

// ConnFromPacketConn adapts a PacketConn with a fixed remote address into a net.Conn.
func ConnFromPacketConn(c net.PacketConn, raddr net.Addr) net.Conn {
	return &packetConnWrapper{inner: c, raddr: raddr}
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

func (c *packetConnWrapper) Read(p []byte) (int, error) {
	for {
		n, raddr, err := c.inner.ReadFrom(p)
		if err != nil {
			return 0, err
		}
		c.mu.Lock()
		if c.raddr == nil {
			c.raddr = raddr
			c.mu.Unlock()
			return n, nil
		}
		same := sameAddr(c.raddr, raddr)
		c.mu.Unlock()
		if same {
			return n, nil
		}
	}
}

func (c *packetConnWrapper) Write(p []byte) (int, error) {
	c.mu.RLock()
	raddr := c.raddr
	c.mu.RUnlock()
	if raddr == nil {
		return 0, errors.New("remote address is not set")
	}
	return c.inner.WriteTo(p, raddr)
}

func (c *packetConnWrapper) Close() error {
	return c.inner.Close()
}

func (c *packetConnWrapper) LocalAddr() net.Addr {
	return c.inner.LocalAddr()
}

func (c *packetConnWrapper) RemoteAddr() net.Addr {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.raddr
}

func (c *packetConnWrapper) SetDeadline(t time.Time) error {
	return c.inner.SetDeadline(t)
}

func (c *packetConnWrapper) SetReadDeadline(t time.Time) error {
	return c.inner.SetReadDeadline(t)
}

func (c *packetConnWrapper) SetWriteDeadline(t time.Time) error {
	return c.inner.SetWriteDeadline(t)
}

func sameAddr(a, b net.Addr) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Network() == b.Network() && a.String() == b.String()
}

// PacketConnFromPacketListener adapts an admitting packet listener into one
// packet socket. When listener admission ends, already accepted packet paths
// remain usable until they end naturally or the returned PacketConn is closed.
// This keeps control-plane liveness independent from established data paths.
func PacketConnFromPacketListener(l PacketListener) net.PacketConn {
	pl := &packetListenerWrapper{
		inner:         l,
		conns:         make(map[string]*packetListenerConn),
		pkts:          make(chan packet, 100),
		done:          make(chan struct{}),
		admissionDone: make(chan struct{}),
		shutdownDone:  make(chan struct{}),
		readDeadline:  newPacketDeadline(),
		writeDeadline: newPacketDeadline(),
	}
	go pl.accept()
	return pl
}

func (pl *packetListenerWrapper) accept() {
	defer func() {
		pl.readers.Wait()
		pl.closeInner()
		close(pl.shutdownDone)
	}()
	for {
		conn, raddr, err := pl.inner.Accept()
		if err != nil {
			pl.stopAdmission(err)
			return
		}
		pl.mu.Lock()
		if pl.closed {
			pl.mu.Unlock()
			_ = conn.Close()
			return
		}
		key := packetAddrKey(raddr)
		entry := &packetListenerConn{conn: conn, addr: raddr}
		entry.active.Store(true)
		previous := pl.conns[key]
		if previous != nil {
			previous.active.Store(false)
		}
		pl.conns[key] = entry
		pl.readers.Add(1)
		pl.mu.Unlock()
		if previous != nil {
			_ = previous.conn.Close()
		}
		deadline, _ := pl.writeDeadline.snapshot()
		if !deadline.IsZero() {
			_ = conn.SetWriteDeadline(deadline)
		}
		go pl.read(entry, key)
	}
}

func (pl *packetListenerWrapper) read(entry *packetListenerConn, key string) {
	defer func() {
		pl.readers.Done()
		entry.active.Store(false)
		_ = entry.conn.Close()
		pl.mu.Lock()
		defer pl.mu.Unlock()
		if pl.closed {
			return
		}
		if pl.conns[key] == entry {
			delete(pl.conns, key)
		}
		if pl.admissionStopped && len(pl.conns) == 0 {
			pl.closed = true
			pl.doneOnce.Do(func() { close(pl.done) })
		}
	}()
	buf := make([]byte, 65535)
	for {
		n, _, err := entry.conn.ReadFrom(buf)
		if err != nil {
			break
		}
		if !entry.active.Load() {
			return
		}
		pkt := packet{data: append([]byte(nil), buf[:n]...), addr: entry.addr, source: entry}
		select {
		case pl.pkts <- pkt:
		case <-pl.done:
			return
		default:
			continue
		}
	}
}

func (pl *packetListenerWrapper) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	for {
		select {
		case <-pl.done:
			return 0, nil, pl.err()
		default:
		}
		deadline, changed := pl.readDeadline.snapshot()
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return 0, nil, os.ErrDeadlineExceeded
		}
		select {
		case pkt := <-pl.pkts:
			if pkt.source != nil && !pkt.source.active.Load() {
				continue
			}
			select {
			case <-pl.done:
				return 0, nil, pl.err()
			default:
			}
			n = copy(p, pkt.data)
			return n, pkt.addr, nil
		case <-pl.done:
			return 0, nil, pl.err()
		case <-changed:
		}
	}
}

func (pl *packetListenerWrapper) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	if pl.writeDeadline.expired() {
		return 0, os.ErrDeadlineExceeded
	}
	pl.mu.Lock()
	if pl.closed {
		pl.mu.Unlock()
		return 0, net.ErrClosed
	}
	entry, ok := pl.conns[packetAddrKey(addr)]
	pl.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("no connection to %v", addr)
	}
	deadline, _ := pl.writeDeadline.snapshot()
	if err := entry.conn.SetWriteDeadline(deadline); err != nil {
		return 0, err
	}
	return entry.conn.WriteTo(p, entry.addr)
}

func (pl *packetListenerWrapper) Close() error {
	pl.initiateShutdown(net.ErrClosed)
	<-pl.shutdownDone
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.shutdownErr
}

func (pl *packetListenerWrapper) initiateShutdown(err error) {
	pl.shutdownOnce.Do(func() {
		pl.mu.Lock()
		pl.closed = true
		if err == nil {
			err = net.ErrClosed
		}
		pl.closeErr = err
		pl.admissionOnce.Do(func() { close(pl.admissionDone) })
		conns := pl.conns
		pl.conns = nil
		for _, entry := range conns {
			entry.active.Store(false)
		}
		pl.doneOnce.Do(func() { close(pl.done) })
		pl.mu.Unlock()
		pl.closeInner()
		for _, entry := range conns {
			_ = entry.conn.Close()
		}
	})
}

func (pl *packetListenerWrapper) stopAdmission(err error) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.closed {
		return
	}
	if err == nil {
		err = net.ErrClosed
	}
	pl.admissionStopped = true
	pl.closeErr = err
	pl.admissionOnce.Do(func() { close(pl.admissionDone) })
	if len(pl.conns) == 0 {
		pl.closed = true
		pl.doneOnce.Do(func() { close(pl.done) })
	}
}

func (pl *packetListenerWrapper) closeInner() {
	pl.innerClose.Do(func() {
		err := pl.inner.Close()
		pl.mu.Lock()
		pl.shutdownErr = err
		pl.mu.Unlock()
	})
}

func packetAddrKey(addr net.Addr) string {
	if addr == nil {
		return "\x00"
	}
	return addr.Network() + "\x00" + addr.String()
}

func (pl *packetListenerWrapper) LocalAddr() net.Addr {
	return pl.inner.Addr()
}

func (pl *packetListenerWrapper) SetDeadline(t time.Time) error {
	if err := pl.SetReadDeadline(t); err != nil {
		return err
	}
	return pl.SetWriteDeadline(t)
}

func (pl *packetListenerWrapper) SetReadDeadline(t time.Time) error {
	pl.mu.Lock()
	closed := pl.closed
	pl.mu.Unlock()
	if closed {
		return net.ErrClosed
	}
	pl.readDeadline.set(t)
	return nil
}

func (pl *packetListenerWrapper) SetWriteDeadline(t time.Time) error {
	pl.mu.Lock()
	if pl.closed {
		pl.mu.Unlock()
		return net.ErrClosed
	}
	conns := make([]net.PacketConn, 0, len(pl.conns))
	for _, entry := range pl.conns {
		conns = append(conns, entry.conn)
	}
	pl.mu.Unlock()
	pl.writeDeadline.set(t)
	var err error
	for _, conn := range conns {
		if deadlineErr := conn.SetWriteDeadline(t); deadlineErr != nil {
			err = errors.Join(err, deadlineErr)
		}
	}
	return err
}

func (pl *packetListenerWrapper) err() error {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.closeErr
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
	slog.With("component", "net").Debug(dir, slog.String("type", string(m.ProtoReflect().Descriptor().FullName())))
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
