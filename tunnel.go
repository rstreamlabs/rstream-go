// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go/pb"
)

type TunnelType string

const (
	TunnelBytestream TunnelType = "bytestream"
	TunnelDatagram   TunnelType = "datagram"
)

type Protocol string

const (
	ProtocolTLS  Protocol = "tls"  // bytestream
	ProtocolDTLS Protocol = "dtls" // datagram
	ProtocolQUIC Protocol = "quic" // datagram
	ProtocolHTTP Protocol = "http" // bytestream (HTTP/1.1, HTTP/2) or datagram (HTTP/3)
)

type TLSMode string

const (
	TLSModePassthrough TLSMode = "passthrough" // For TLS tunnels only
	TLSModeTerminated  TLSMode = "terminated"
)

type HTTPVersion string

const (
	HTTP1_1 HTTPVersion = "http/1.1" // HTTP/1.1 (cleartext)
	HTTP2   HTTPVersion = "h2c"      // HTTP/2 (cleartext)
	HTTP3   HTTPVersion = "h3"       // HTTP/3
)

type TunnelProperties struct {
	// Basic tunnel properties
	ID           *string
	Name         *string
	CreationDate *time.Time

	// Tunnel options
	Type     *TunnelType
	Publish  *bool
	Protocol *Protocol // Only for published tunnels
	Labels   map[string]string

	// Security options
	GeoIP      []string
	TrustedIPs []string
	Domain     *string // Only for published tunnels

	// TLS options
	TLSMode       *TLSMode // Passthorugh is only supported for TLS tunnels
	TLSALPNs      []string
	TLSMinVersion *uint16
	TLSCiphers    []uint16
	MTLS          *bool
	MTLSCACertPEM *string

	// HTTP tunnel options (only for HTTP tunnels)
	HTTPVersion    *HTTPVersion
	HTTPUseTLS     *bool // Only for HTTP/1.1 and HTTP/2 (bytestream)
	TokenAuth      *bool
	SSO            *bool
	SSOProviders   []string
	EmailWhitelist []string
	EmailBlacklist []string
	Challenge      *bool
}

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
	t.closeCh <- err
	close(t.closeCh)
}

type bytestreamConn struct {
	conn net.Conn
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

func (bc *bytestreamConn) LocalAddr() net.Addr {
	return bc.conn.LocalAddr()
}

func (bc *bytestreamConn) RemoteAddr() net.Addr {
	return bc.conn.RemoteAddr()
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
	net.PacketConn
}

type dtPacket struct {
	data []byte
	from net.Addr
}

type dtSession struct {
	conn   net.Conn
	w      *bufio.Writer
	r      *bufio.Reader
	raddr  net.Addr
	parent *datagramTunnelImpl
	doneCh chan struct{}
}

type datagramTunnelImpl struct {
	tunnel   BytestreamTunnel
	sessions map[string]*dtSession
	incoming chan dtPacket
	mu       sync.Mutex
	closed   bool
}

func newDatagramTunnel(t BytestreamTunnel) *datagramTunnelImpl {
	d := &datagramTunnelImpl{
		tunnel:   t,
		sessions: make(map[string]*dtSession),
		incoming: make(chan dtPacket, 100),
	}
	go d.accept()
	return d
}

func (d *datagramTunnelImpl) accept() {
	for {
		conn, err := d.tunnel.Accept()
		if err != nil {
			d.mu.Lock()
			if d.closed {
				d.mu.Unlock()
				return
			}
			d.mu.Unlock()
			fmt.Println("Accept error:", err) // TODO : Treat error as fatal
			return
		}
		raddr := conn.RemoteAddr()
		s := &dtSession{
			conn:   conn,
			w:      bufio.NewWriter(conn),
			r:      bufio.NewReader(conn),
			raddr:  raddr,
			parent: d,
			doneCh: make(chan struct{}),
		}
		d.mu.Lock()
		d.sessions[raddr.String()] = s
		d.mu.Unlock()
		go s.read()
	}
}

func (t *datagramTunnelImpl) ForwardingAddress() (string, error) {
	return t.tunnel.ForwardingAddress()
}

func (t *datagramTunnelImpl) Properties() (TunnelProperties, error) {
	return t.tunnel.Properties()
}

func (t *datagramTunnelImpl) ReadFrom(p []byte) (int, net.Addr, error) {
	pkt, ok := <-t.incoming
	if !ok {
		return 0, nil, net.ErrClosed
	}
	if len(pkt.data) > len(p) {
		copy(p, pkt.data[:len(p)])
		return len(p), pkt.from, fmt.Errorf("datagram truncated")
	} else {
		copy(p, pkt.data)
		return len(pkt.data), pkt.from, nil
	}
}

func (t *datagramTunnelImpl) WriteTo(p []byte, addr net.Addr) (int, error) {
	t.mu.Lock()
	s, ok := t.sessions[addr.String()]
	t.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("no session for remote %s", addr)
	}
	return s.write(p)
}

func (t *datagramTunnelImpl) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	for _, s := range t.sessions {
		s.conn.Close()
	}
	t.sessions = nil
	t.tunnel.Close()
	close(t.incoming)
	return nil
}

func (t *datagramTunnelImpl) LocalAddr() net.Addr {
	return t.tunnel.Addr()
}

func (t *datagramTunnelImpl) SetDeadline(time time.Time) error {
	return errors.New("not implemented yet")
}

func (t *datagramTunnelImpl) SetReadDeadline(time time.Time) error {
	return errors.New("not implemented yet")
}

func (t *datagramTunnelImpl) SetWriteDeadline(time time.Time) error {
	return errors.New("not implemented yet")
}

func (s *dtSession) read() {
	defer func() {
		s.close()
	}()
	for {
		data, err := readMessage(s.r)
		if err != nil {
			fmt.Println("read error, closing session:", err)
			break
		}
		pkt := dtPacket{data: data, from: s.raddr}
		select {
		case s.parent.incoming <- pkt:
		default:
			fmt.Println("incoming datagram queue is full, dropping packet")
			return
		}
	}
}

func (s *dtSession) write(p []byte) (int, error) {
	err := writeMessage(s.w, p)
	if err != nil {
		return 0, err
	} else {
		return len(p), nil
	}
}

func (s *dtSession) close() {
	s.conn.Close()
	s.parent.mu.Lock()
	delete(s.parent.sessions, s.raddr.String())
	s.parent.mu.Unlock()
	close(s.doneCh)
}
