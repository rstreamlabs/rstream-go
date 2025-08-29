// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
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

type connWrapper struct {
	inner net.Conn
	w     *bufio.Writer
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
	return &connWrapper{
		inner: conn,
		w:     bufio.NewWriter(conn),
		r:     bufio.NewReader(conn),
		raddr: raddr,
	}, raddr, nil
}

func (l *listenerWrapper) Close() error {
	return l.inner.Close()
}

func (l *listenerWrapper) Addr() net.Addr {
	return l.inner.Addr()
}

func PacketConnFromConn(c net.Conn) net.PacketConn {
	return &connWrapper{inner: c}
}

func (c *connWrapper) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	n, err = c.read(p)
	return n, c.raddr, err
}

func (c *connWrapper) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	if addr.String() != c.raddr.String() {
		return 0, fmt.Errorf("invalid address: expected %v, got %v", c.raddr, addr)
	}
	return c.write(p)
}

func (c *connWrapper) read(p []byte) (int, error) {
	msg, err := readMessage(c.r)
	if err != nil {
		return 0, err
	}
	n := copy(p, msg)
	return n, nil
}

func (c *connWrapper) write(p []byte) (int, error) {
	err := writeMessage(c.w, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
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
