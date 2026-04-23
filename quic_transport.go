// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// QUICTransport is a stateful Dialer that multiplexes all connections over a
// single QUIC connection. The first call to Dial establishes the underlying
// QUIC connection; subsequent calls open a new QUIC stream on that connection.
// It also implements DatagramProvider so that datagram tunnels can bypass the
// 4-byte framing overhead used by stream-based tunnels.
//
// No reconnection on error — let the control channel error propagate naturally.
type QUICTransport struct {
	LocalAddr     *string
	ForceIPv4     *bool
	ForceIPv6     *bool
	DNSOverride   *string
	DNSOverTLS    *bool
	DNSServerName *string
	DNSSECEnabled *bool
	mu            sync.Mutex
	quicConn      *quic.Conn
	pconn         net.PacketConn
}

// Dial establishes or reuses a QUIC connection to addr, then opens and returns
// a new QUIC stream wrapped as a net.Conn.
func (t *QUICTransport) Dial(ctx context.Context, addr string, tlsCfg *tls.Config) (net.Conn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.quicConn == nil {
		conn, pconn, err := t.connect(ctx, addr, tlsCfg)
		if err != nil {
			return nil, err
		}
		t.quicConn = conn
		t.pconn = pconn
	}
	stream, err := t.quicConn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open QUIC stream: %w", err)
	}
	return &quicStreamConn{stream: stream, conn: t.quicConn}, nil
}

// SendDatagram sends a datagram over the underlying QUIC connection.
// It implements DatagramProvider.
func (t *QUICTransport) SendDatagram(data []byte) error {
	t.mu.Lock()
	conn := t.quicConn
	t.mu.Unlock()
	if conn == nil {
		return errors.New("QUIC connection not established")
	}
	return conn.SendDatagram(data)
}

// ReceiveDatagram receives a datagram from the underlying QUIC connection.
// It implements DatagramProvider.
func (t *QUICTransport) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	t.mu.Lock()
	conn := t.quicConn
	t.mu.Unlock()
	if conn == nil {
		return nil, errors.New("QUIC connection not established")
	}
	return conn.ReceiveDatagram(ctx)
}

// Close closes the underlying QUIC connection and the UDP socket beneath it.
func (t *QUICTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.quicConn == nil {
		return nil
	}
	err := t.quicConn.CloseWithError(0, "transport closed")
	t.quicConn = nil
	if t.pconn != nil {
		if pcErr := t.pconn.Close(); pcErr != nil && err == nil {
			err = pcErr
		}
		t.pconn = nil
	}
	return err
}

// connect creates the underlying QUIC connection. Must be called with t.mu held.
// It returns both the QUIC connection and the underlying UDP socket so the
// caller can close the socket when the connection is torn down.
func (t *QUICTransport) connect(ctx context.Context, addr string, tlsCfg *tls.Config) (*quic.Conn, net.PacketConn, error) {
	network := "udp"
	if t.ForceIPv4 != nil && *t.ForceIPv4 {
		network = "udp4"
	} else if t.ForceIPv6 != nil && *t.ForceIPv6 {
		network = "udp6"
	}
	dialAddr := addr
	dnsOpts := dnsResolverOptionsFromQUICTransport(t)
	var err error
	if dnsOpts.enabled() {
		dialAddr, err = resolveDialAddress(ctx, addr, dnsOpts)
		if err != nil {
			return nil, nil, err
		}
	}
	// Bind to a local UDP address.
	var localUDPAddr *net.UDPAddr
	if t.LocalAddr != nil {
		ip := net.ParseIP(*t.LocalAddr)
		if ip == nil {
			return nil, nil, fmt.Errorf("failed to parse local address %q", *t.LocalAddr)
		}
		localUDPAddr = &net.UDPAddr{IP: ip}
	}
	localNetwork := network
	var pconn net.PacketConn
	if localUDPAddr != nil {
		pconn, err = net.ListenPacket(localNetwork, localUDPAddr.String())
	} else {
		pconn, err = net.ListenPacket(localNetwork, ":0")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create UDP socket: %w", err)
	}
	// Resolve the remote address for quic.Dial.
	udpAddr, err := net.ResolveUDPAddr(network, dialAddr)
	if err != nil {
		pconn.Close()
		return nil, nil, fmt.Errorf("failed to resolve UDP address %q: %w", dialAddr, err)
	}
	quicCfg := &quic.Config{
		EnableDatagrams: true,
	}
	conn, err := quic.Dial(ctx, pconn, udpAddr, tlsCfg, quicCfg)
	if err != nil {
		pconn.Close()
		return nil, nil, fmt.Errorf("failed to establish QUIC connection: %w", err)
	}
	return conn, pconn, nil
}

// quicStreamConn wraps a quic.Stream as a net.Conn, delegating LocalAddr and
// RemoteAddr to the underlying quic.Conn.
type quicStreamConn struct {
	stream *quic.Stream
	conn   *quic.Conn
}

func (c *quicStreamConn) Read(p []byte) (int, error) {
	return c.stream.Read(p)
}

func (c *quicStreamConn) Write(p []byte) (int, error) {
	return c.stream.Write(p)
}

func (c *quicStreamConn) Close() error {
	// CancelRead sends STOP_SENDING so the remote stops sending and any
	// pending Read call returns immediately, giving a true bidirectional close.
	c.stream.CancelRead(0)
	return c.stream.Close()
}

func (c *quicStreamConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *quicStreamConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *quicStreamConn) SetDeadline(t time.Time) error {
	return c.stream.SetDeadline(t)
}

func (c *quicStreamConn) SetReadDeadline(t time.Time) error {
	return c.stream.SetReadDeadline(t)
}

func (c *quicStreamConn) SetWriteDeadline(t time.Time) error {
	return c.stream.SetWriteDeadline(t)
}

// datagramChannelIDFromStreamID derives a deterministic 4-byte channel ID from
// a stream ID. The engine generates stream IDs as 24-character lowercase hex
// strings; we parse the first 8 hex characters (4 bytes) and interpret them as
// a big-endian uint32. This mirrors the engine's own datagramChannelIDFromStreamID
// implementation so that both sides compute the same channel ID for a given
// stream.
func datagramChannelIDFromStreamID(streamID string) uint32 {
	if len(streamID) < 8 {
		return 0
	}
	var b [4]byte
	for i := 0; i < 4; i++ {
		b[i] = (hexNibble(streamID[i*2]) << 4) | hexNibble(streamID[i*2+1])
	}
	return binary.BigEndian.Uint32(b[:])
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0
	}
}

// quicDatagramChannel implements net.PacketConn for a single datagram tunnel
// channel identified by a 4-byte channel ID. Datagrams sent on WriteTo are
// prefixed with the channel ID; incoming datagrams are received on recvCh after
// the datagramReadLoop strips the channel ID prefix and routes them here.
type quicDatagramChannel struct {
	channelID uint32
	provider  DatagramProvider
	laddr     net.Addr
	raddr     net.Addr
	recvCh    chan []byte
	ctx       context.Context
	cancel    context.CancelFunc
}

func (c *quicDatagramChannel) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data, ok := <-c.recvCh:
		if !ok {
			return 0, nil, net.ErrClosed
		}
		n := copy(p, data)
		return n, c.laddr, nil
	case <-c.ctx.Done():
		return 0, nil, net.ErrClosed
	}
}

func (c *quicDatagramChannel) WriteTo(p []byte, addr net.Addr) (int, error) {
	// Prepend 4-byte big-endian channel ID.
	buf := make([]byte, 4+len(p))
	binary.BigEndian.PutUint32(buf[:4], c.channelID)
	copy(buf[4:], p)
	if err := c.provider.SendDatagram(buf); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *quicDatagramChannel) Close() error {
	c.cancel()
	return nil
}

func (c *quicDatagramChannel) LocalAddr() net.Addr {
	return c.laddr
}

func (c *quicDatagramChannel) SetDeadline(t time.Time) error {
	return nil
}

func (c *quicDatagramChannel) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *quicDatagramChannel) SetWriteDeadline(t time.Time) error {
	return nil
}

// quicDatagramListener implements PacketListener for datagram tunnels backed by
// QUIC datagrams. Each incoming ProxyConnReq delivers a quicDatagramChannel to
// the conns channel; Accept returns them one at a time.
type quicDatagramListener struct {
	conns  chan net.PacketConn
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	laddr  net.Addr
}

func (l *quicDatagramListener) Accept() (net.PacketConn, net.Addr, error) {
	select {
	case conn, ok := <-l.conns:
		if !ok {
			return nil, nil, net.ErrClosed
		}
		return conn, conn.LocalAddr(), nil
	case <-l.ctx.Done():
		return nil, nil, net.ErrClosed
	}
}

func (l *quicDatagramListener) Close() error {
	l.once.Do(func() { l.cancel() })
	return nil
}

func (l *quicDatagramListener) Addr() net.Addr {
	return l.laddr
}
