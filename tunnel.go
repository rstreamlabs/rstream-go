// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/rstreamlabs/rstream-go/pb"
)

type Tunnel interface {
	ForwardingAddress() (string, error)
	Properties() (TunnelProperties, error)
	Close() error
}

type BytestreamTunnel interface {
	Tunnel
	net.Listener
}

type bytestreamTunnelImpl struct {
	props    TunnelProperties
	ctrl     *controlChannelImpl
	tunnelID string
	closeCh  chan error
	closing  bool
	closed   bool
	err      error
	conns    chan net.Conn
	ctx      context.Context
	cancel   context.CancelFunc
}

func (t *bytestreamTunnelImpl) ForwardingAddress() (string, error) {
	return FormatForwardingAddr(t.props)
}

func (t *bytestreamTunnelImpl) Properties() (TunnelProperties, error) {
	return t.props, nil
}

func (t *bytestreamTunnelImpl) Accept() (net.Conn, error) {
	t.ctrl.mu.Lock()
	if t.closed {
		t.ctrl.mu.Unlock()
		return nil, net.ErrClosed
	}
	t.ctrl.mu.Unlock()
	select {
	case conn := <-t.conns:
		return conn, nil
	case err := <-t.closeCh:
		if err == nil {
			return nil, net.ErrClosed
		}
		return nil, err
	}
}

func (t *bytestreamTunnelImpl) Addr() net.Addr {
	return &Addr{
		IdOrName: t.tunnelID,
	}
}

func (t *bytestreamTunnelImpl) Close() error {
	t.ctrl.mu.Lock()
	if t.closed {
		t.ctrl.mu.Unlock()
		return nil
	}
	if t.closing == false {
		t.closing = true
		go func() {
			msg := &pb.Message{
				Payload: &pb.Message_CloseTunnelReq{
					CloseTunnelReq: &pb.CloseTunnelReq{
						TunnelId: t.tunnelID,
					},
				},
			}
			if err := t.ctrl.writePbMessage(msg); err != nil {
				t.ctrl.mu.Lock()
				t.ctrl.onError(fmt.Errorf("failed to send CloseTunnelReq: %w", err))
				t.ctrl.mu.Unlock()
			}
		}()
	}
	t.ctrl.mu.Unlock()
	select {
	case err := <-t.closeCh:
		return err
	case <-t.ctrl.doneCh:
		return errors.New("control channel closed")
	}
}

func (t *bytestreamTunnelImpl) onClose() {
	if t.closed {
		return
	}
	t.onError(nil)
}

func (t *bytestreamTunnelImpl) onError(err error) {
	if t.closed {
		return
	}
	t.closed = true
	t.err = err
	if t.cancel != nil {
		t.cancel()
	}
	t.closeCh <- err
	close(t.closeCh)
}

type bytestreamConn struct {
	conn  net.Conn
	laddr Addr
	raddr Addr
}

func (bc *bytestreamConn) Read(p []byte) (int, error) {
	return bc.conn.Read(p)
}

func (bc *bytestreamConn) Write(p []byte) (int, error) {
	return bc.conn.Write(p)
}

func (bc *bytestreamConn) Close() error {
	return bc.conn.Close()
}

func (bc *bytestreamConn) CloseWrite() error {
	if conn, ok := bc.conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return bc.conn.Close()
}

func (bc *bytestreamConn) LocalAddr() net.Addr {
	return &bc.laddr
}

func (bc *bytestreamConn) RemoteAddr() net.Addr {
	return &bc.raddr
}

func (bc *bytestreamConn) SetDeadline(t time.Time) error {
	return bc.conn.SetDeadline(t)
}

func (bc *bytestreamConn) SetReadDeadline(t time.Time) error {
	return bc.conn.SetReadDeadline(t)
}

func (bc *bytestreamConn) SetWriteDeadline(t time.Time) error {
	return bc.conn.SetWriteDeadline(t)
}

type DatagramTunnel interface {
	Tunnel
	PacketListener
}

type datagramTunnelImpl struct {
	inner Tunnel
	pl    PacketListener
}

func (t *datagramTunnelImpl) ForwardingAddress() (string, error) {
	return t.inner.ForwardingAddress()
}

func (t *datagramTunnelImpl) Properties() (TunnelProperties, error) {
	return t.inner.Properties()
}

func (t *datagramTunnelImpl) Close() error {
	return t.inner.Close()
}

func (t *datagramTunnelImpl) Accept() (net.PacketConn, net.Addr, error) {
	return t.pl.Accept()
}

func (t *datagramTunnelImpl) Addr() net.Addr {
	return t.pl.Addr()
}
