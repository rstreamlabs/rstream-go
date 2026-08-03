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
	closedCh chan struct{}
	closing  bool
	closed   bool
	err      error
	conns    chan net.Conn
	ctx      context.Context
	cancel   context.CancelFunc
}

type tunnelCleanup struct {
	cancel context.CancelFunc
	conns  []net.Conn
}

func (c tunnelCleanup) run() {
	if c.cancel != nil {
		c.cancel()
	}
	for _, conn := range c.conns {
		_ = conn.Close()
	}
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
		err := t.acceptErrorLocked()
		t.ctrl.mu.Unlock()
		return nil, err
	}
	closedCh := t.ensureClosedSignalLocked()
	t.ctrl.mu.Unlock()
	select {
	case conn := <-t.conns:
		t.ctrl.mu.Lock()
		if t.closed {
			err := t.acceptErrorLocked()
			t.ctrl.mu.Unlock()
			_ = conn.Close()
			return nil, err
		}
		t.ctrl.mu.Unlock()
		return conn, nil
	case <-closedCh:
		t.ctrl.mu.Lock()
		err := t.acceptErrorLocked()
		t.ctrl.mu.Unlock()
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
		err := t.err
		t.ctrl.mu.Unlock()
		return err
	}
	closedCh := t.ensureClosedSignalLocked()
	sendClose := !t.closing
	if sendClose {
		t.closing = true
	}
	t.ctrl.mu.Unlock()
	ctx, cancel := t.ctrl.newCloseContext()
	defer cancel()
	if sendClose {
		msg := &pb.Message{
			Payload: &pb.Message_CloseTunnelReq{
				CloseTunnelReq: &pb.CloseTunnelReq{
					TunnelId: t.tunnelID,
				},
			},
		}
		if err := t.ctrl.writePbMessageContext(ctx, msg); err != nil {
			closeErr := fmt.Errorf("failed to send CloseTunnelReq: %w", err)
			t.ctrl.onError(closeErr)
			return closeErr
		}
	}
	select {
	case <-closedCh:
		return t.closeResult()
	case <-t.ctrl.closedCh:
		return t.closeResultAfterControlClosed(closedCh)
	case <-ctx.Done():
		select {
		case <-closedCh:
			return t.closeResult()
		case <-t.ctrl.closedCh:
			return t.closeResultAfterControlClosed(closedCh)
		default:
		}
		closeErr := fmt.Errorf("timed out waiting for tunnel %q to close: %w", t.tunnelID, context.Cause(ctx))
		t.ctrl.onError(closeErr)
		return closeErr
	}
}

func (t *bytestreamTunnelImpl) closeResult() error {
	t.ctrl.mu.Lock()
	defer t.ctrl.mu.Unlock()
	return t.err
}

func (t *bytestreamTunnelImpl) closeResultAfterControlClosed(closedCh <-chan struct{}) error {
	// A CloseTunnelRsp and the subsequent control-channel shutdown can become
	// visible to this goroutine at the same time. The tunnel acknowledgement is
	// the more specific result and must win over the transport EOF.
	select {
	case <-closedCh:
		return t.closeResult()
	default:
	}
	if err := t.ctrl.Err(); err != nil {
		return fmt.Errorf("control channel closed: %w", err)
	}
	return errors.New("control channel closed")
}

func (t *bytestreamTunnelImpl) onCloseLocked() tunnelCleanup {
	if t.closed {
		return tunnelCleanup{}
	}
	return t.onErrorLocked(nil)
}

func (t *bytestreamTunnelImpl) onError(err error) {
	t.ctrl.mu.Lock()
	cleanup := t.onErrorLocked(err)
	t.ctrl.mu.Unlock()
	cleanup.run()
}

func (t *bytestreamTunnelImpl) onErrorLocked(err error) tunnelCleanup {
	if t.closed {
		return tunnelCleanup{}
	}
	t.closed = true
	t.err = err
	close(t.ensureClosedSignalLocked())
	cleanup := tunnelCleanup{cancel: t.cancel}
	t.cancel = nil
	for {
		select {
		case conn := <-t.conns:
			cleanup.conns = append(cleanup.conns, conn)
		default:
			return cleanup
		}
	}
}

func (t *bytestreamTunnelImpl) acceptErrorLocked() error {
	if t.err != nil {
		return t.err
	}
	return net.ErrClosed
}

func (t *bytestreamTunnelImpl) ensureClosedSignalLocked() chan struct{} {
	if t.closedCh == nil {
		t.closedCh = make(chan struct{})
	}
	return t.closedCh
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
