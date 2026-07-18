// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// ErrDatagramTooLarge is returned by datagram channel writes when the payload
// exceeds what the underlying QUIC connection can carry in a single datagram.
var ErrDatagramTooLarge = errors.New("datagram payload too large")

const datagramChannelIDSize = 12

type datagramChannelID [datagramChannelIDSize]byte

// QUICTransport is a stateful Dialer that multiplexes all connections over a
// single QUIC connection. The first call to Dial establishes the underlying
// QUIC connection; subsequent calls open a new QUIC stream on that connection.
// It also implements DatagramProvider so that datagram tunnels can bypass the
// 4-byte framing overhead used by stream-based tunnels.
//
// No reconnection on error — let the control channel error propagate naturally.
type QUICTransport struct {
	LocalAddr            *string
	NetworkInterface     *string
	ForceIPv4            *bool
	ForceIPv6            *bool
	DNSOverride          *string
	DNSOverTLS           *bool
	DNSServerName        *string
	DNSSECEnabled        *bool
	ProxyHTTP            *string
	ProxySOCKS5          *string
	ProxyUsername        *string
	ProxyPassword        *string
	ProxyHTTPHeaders     map[string]string
	TLSProxyConfig       *tls.Config
	ProxyFromEnvironment *bool
	mu                   sync.Mutex
	connectMu            sync.Mutex
	quicConn             *quic.Conn
	qtransport           *quic.Transport
	pconn                net.PacketConn
	proxyCloser          io.Closer
	origin               string
	closeGeneration      uint64
	datagramChannels     map[datagramChannelID]*quicDatagramChannel
	datagramReadRunning  bool
}

// Dial establishes or reuses a QUIC connection to addr, then opens and returns
// a new QUIC stream wrapped as a net.Conn.
func (t *QUICTransport) Dial(ctx context.Context, addr string, tlsCfg *tls.Config) (net.Conn, error) {
	origin, err := quicTransportOrigin(addr, tlsCfg)
	if err != nil {
		return nil, err
	}
	var openErr error
	for attempt := 0; attempt < 2; attempt++ {
		conn, err := t.connection(ctx, addr, tlsCfg, origin)
		if err != nil {
			return nil, err
		}
		stream, err := conn.OpenStreamSync(ctx)
		if err == nil {
			return &quicStreamConn{stream: stream, conn: conn, transport: t}, nil
		}
		openErr = err
		if ctx.Err() != nil || conn.Context().Err() == nil || !t.invalidateConnection(conn) {
			return nil, fmt.Errorf("failed to open QUIC stream: %w", err)
		}
	}
	return nil, fmt.Errorf("failed to open QUIC stream: %w", openErr)
}

func (t *QUICTransport) connection(ctx context.Context, addr string, tlsCfg *tls.Config, origin string) (*quic.Conn, error) {
	t.mu.Lock()
	if t.quicConn != nil {
		if t.origin != origin {
			t.mu.Unlock()
			return nil, fmt.Errorf("QUIC transport already connected to %s", t.origin)
		}
		conn := t.quicConn
		t.mu.Unlock()
		return conn, nil
	}
	t.mu.Unlock()
	t.connectMu.Lock()
	defer t.connectMu.Unlock()
	t.mu.Lock()
	if t.quicConn != nil {
		if t.origin != origin {
			t.mu.Unlock()
			return nil, fmt.Errorf("QUIC transport already connected to %s", t.origin)
		}
		conn := t.quicConn
		t.mu.Unlock()
		return conn, nil
	}
	generation := t.closeGeneration
	t.mu.Unlock()
	conn, qtransport, pconn, proxyCloser, err := t.connect(ctx, addr, tlsCfg)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.closeGeneration != generation {
		t.mu.Unlock()
		_ = conn.CloseWithError(0, "transport closed")
		_ = closeQUICTransportResources(qtransport, pconn, proxyCloser)
		return nil, net.ErrClosed
	}
	t.quicConn = conn
	t.qtransport = qtransport
	t.pconn = pconn
	t.proxyCloser = proxyCloser
	t.origin = origin
	t.mu.Unlock()
	return conn, nil
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
	err := conn.SendDatagram(data)
	t.invalidateConnectionOnError(conn, err)
	return err
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
	data, err := conn.ReceiveDatagram(ctx)
	t.invalidateConnectionOnError(conn, err)
	return data, err
}

// Close closes the underlying QUIC connection and the UDP socket beneath it.
func (t *QUICTransport) Close() error {
	t.mu.Lock()
	t.closeGeneration++
	channels := t.detachDatagramChannelsLocked()
	if t.quicConn == nil {
		t.mu.Unlock()
		for _, ch := range channels {
			ch.Close()
		}
		return nil
	}
	err := t.quicConn.CloseWithError(0, "transport closed")
	t.quicConn = nil
	t.origin = ""
	if closeErr := closeQUICTransportResources(t.qtransport, t.pconn, t.proxyCloser); closeErr != nil && err == nil {
		err = closeErr
	}
	t.qtransport = nil
	t.pconn = nil
	t.proxyCloser = nil
	t.mu.Unlock()
	for _, ch := range channels {
		ch.Close()
	}
	return err
}

func (t *QUICTransport) invalidateConnection(conn *quic.Conn) bool {
	t.mu.Lock()
	if t.quicConn != conn {
		t.mu.Unlock()
		return false
	}
	t.closeGeneration++
	channels := t.detachDatagramChannelsLocked()
	qtransport := t.qtransport
	pconn := t.pconn
	proxyCloser := t.proxyCloser
	t.quicConn = nil
	t.qtransport = nil
	t.pconn = nil
	t.proxyCloser = nil
	t.origin = ""
	t.mu.Unlock()
	_ = conn.CloseWithError(0, "transport connection unavailable")
	_ = closeQUICTransportResources(qtransport, pconn, proxyCloser)
	for _, ch := range channels {
		ch.Close()
	}
	return true
}

func (t *QUICTransport) invalidateConnectionOnError(conn *quic.Conn, err error) {
	if err != nil && conn.Context().Err() != nil {
		t.invalidateConnection(conn)
	}
}

func (t *QUICTransport) registerDatagramChannel(id datagramChannelID, ch *quicDatagramChannel) bool {
	t.mu.Lock()
	if t.quicConn == nil {
		t.mu.Unlock()
		return false
	}
	if t.datagramChannels == nil {
		t.datagramChannels = make(map[datagramChannelID]*quicDatagramChannel)
	}
	if t.datagramChannels[id] != nil {
		t.mu.Unlock()
		return false
	}
	t.datagramChannels[id] = ch
	conn := t.quicConn
	start := !t.datagramReadRunning
	if start {
		t.datagramReadRunning = true
	}
	t.mu.Unlock()
	if start {
		go t.datagramReadLoop(conn)
	}
	return true
}

func (t *QUICTransport) unregisterDatagramChannel(id datagramChannelID, ch *quicDatagramChannel) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.datagramChannels != nil && t.datagramChannels[id] == ch {
		delete(t.datagramChannels, id)
	}
}

func (t *QUICTransport) datagramReadLoop(conn *quic.Conn) {
	for {
		data, err := conn.ReceiveDatagram(conn.Context())
		if err != nil {
			if !t.invalidateConnection(conn) {
				t.closeDatagramChannelsForConn(conn)
			}
			return
		}
		if len(data) < datagramChannelIDSize {
			continue
		}
		var channelID datagramChannelID
		copy(channelID[:], data[:datagramChannelIDSize])
		payload := data[datagramChannelIDSize:]
		t.mu.Lock()
		ch := t.datagramChannels[channelID]
		t.mu.Unlock()
		if ch != nil {
			select {
			case ch.recvCh <- payload:
			default:
			}
		}
	}
}

func (t *QUICTransport) closeDatagramChannelsForConn(conn *quic.Conn) {
	t.mu.Lock()
	if t.quicConn != nil && t.quicConn != conn {
		t.mu.Unlock()
		return
	}
	if t.quicConn == conn {
		t.datagramReadRunning = false
	}
	channels := t.detachDatagramChannelsLocked()
	t.mu.Unlock()
	for _, ch := range channels {
		ch.Close()
	}
}

func (t *QUICTransport) detachDatagramChannelsLocked() []*quicDatagramChannel {
	channels := make([]*quicDatagramChannel, 0, len(t.datagramChannels))
	for _, ch := range t.datagramChannels {
		channels = append(channels, ch)
	}
	t.datagramChannels = nil
	t.datagramReadRunning = false
	return channels
}

func closeQUICTransportResources(qtransport *quic.Transport, pconn net.PacketConn, proxyCloser io.Closer) error {
	var err error
	if qtransport != nil {
		if transportErr := closeOwned(qtransport); transportErr != nil {
			err = transportErr
		}
	}
	if pconn != nil {
		if pcErr := closeOwned(pconn); pcErr != nil && err == nil {
			err = pcErr
		}
	}
	if proxyCloser != nil {
		if proxyErr := closeOwned(proxyCloser); proxyErr != nil && err == nil {
			err = proxyErr
		}
	}
	return err
}

func closeOwned(closer io.Closer) error {
	if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func quicTransportOrigin(addr string, tlsCfg *tls.Config) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.Contains(addr, ":") {
			return "", err
		}
		host = addr
	}
	serverName := ""
	nextProtos := ""
	if tlsCfg != nil {
		serverName = tlsCfg.ServerName
		nextProtos = strings.Join(tlsCfg.NextProtos, ",")
	}
	return strings.ToLower(host) + ":" + port + "|" + strings.ToLower(serverName) + "|" + nextProtos, nil
}

// connect creates the underlying QUIC connection. It returns the QUIC
// transport, packet connection, and optional proxy closer so the caller can
// tear down every owned resource when the connection is closed.
func (t *QUICTransport) connect(ctx context.Context, addr string, tlsCfg *tls.Config) (*quic.Conn, *quic.Transport, net.PacketConn, io.Closer, error) {
	network := "udp"
	if t.ForceIPv4 != nil && *t.ForceIPv4 {
		network = "udp4"
	} else if t.ForceIPv6 != nil && *t.ForceIPv6 {
		network = "udp6"
	}
	dialAddr := addr
	dnsOpts := dnsResolverOptionsFromQUICTransport(t)
	var err error
	proxyHTTP, proxySOCKS5, err := effectiveProxyURLs(proxyValue(t.ProxyHTTP), proxyValue(t.ProxySOCKS5), t.ProxyFromEnvironment, addr)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if proxyHTTP != "" && proxySOCKS5 != "" {
		return nil, nil, nil, nil, errors.New("only one proxy transport can be configured")
	}
	if proxyHTTP == "" && proxySOCKS5 == "" && t.TLSProxyConfig != nil {
		return nil, nil, nil, nil, errors.New("TLS proxy configuration requires an HTTP or environment proxy")
	}
	if proxySOCKS5 == "" && proxyHTTP == "" && dnsOpts.enabled() {
		dialAddr, err = resolveDialAddress(ctx, addr, dnsOpts)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	// Bind to a local UDP address.
	var localUDPAddr *net.UDPAddr
	if t.LocalAddr != nil {
		ip := net.ParseIP(*t.LocalAddr)
		if ip == nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to parse local address %q", *t.LocalAddr)
		}
		localUDPAddr = &net.UDPAddr{IP: ip}
	} else if t.NetworkInterface != nil {
		ip, err := selectInterfaceIP(*t.NetworkInterface, boolValue(t.ForceIPv4), boolValue(t.ForceIPv6))
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if ip == nil {
			return nil, nil, nil, nil, errors.New("no matching IP for selected interface")
		}
		localUDPAddr = &net.UDPAddr{IP: ip}
	}
	var pconn net.PacketConn
	quicCfg := &quic.Config{
		EnableDatagrams: true,
	}
	var remoteAddr net.Addr
	var proxyCloser io.Closer
	if proxyHTTP != "" {
		quicCfg.InitialPacketSize = 1200
		pconn, proxyCloser, remoteAddr, err = t.connectHTTPProxy(ctx, proxyHTTP, addr, tlsCfg, quicCfg, dnsOpts, network, localUDPAddr)
	} else if proxySOCKS5 != "" {
		if t.TLSProxyConfig != nil {
			return nil, nil, nil, nil, errors.New("TLS proxy configuration cannot be used with SOCKS5 proxy")
		}
		quicCfg.InitialPacketSize = 1200
		tcpDialer := &net.Dialer{}
		if localUDPAddr != nil {
			tcpDialer.LocalAddr = &net.TCPAddr{IP: localUDPAddr.IP}
		}
		pconn, remoteAddr, err = newSOCKS5UDPConn(ctx, tcpDialer, network, strings.Replace(network, "udp", "tcp", 1), localUDPAddr, proxySOCKS5, addr, dnsOpts, t.ProxyUsername, t.ProxyPassword)
	} else {
		pconn, remoteAddr, err = t.directPacketConn(network, localUDPAddr, dialAddr)
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	qtransport := &quic.Transport{Conn: pconn}
	conn, err := qtransport.Dial(ctx, remoteAddr, tlsCfg, quicCfg)
	if err != nil {
		_ = closeQUICTransportResources(qtransport, pconn, proxyCloser)
		return nil, nil, nil, nil, fmt.Errorf("failed to establish QUIC connection: %w", err)
	}
	return conn, qtransport, pconn, proxyCloser, nil
}

// quicStreamConn wraps a quic.Stream as a net.Conn, delegating LocalAddr and
// RemoteAddr to the underlying quic.Conn.
type quicStreamConn struct {
	stream    *quic.Stream
	conn      *quic.Conn
	transport *QUICTransport
}

func (c *quicStreamConn) Read(p []byte) (int, error) {
	n, err := c.stream.Read(p)
	c.transport.invalidateConnectionOnError(c.conn, err)
	return n, err
}

func (c *quicStreamConn) Write(p []byte) (int, error) {
	n, err := c.stream.Write(p)
	c.transport.invalidateConnectionOnError(c.conn, err)
	return n, err
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

// datagramChannelIDFromStreamID derives the datagram routing key from the full
// 12-byte stream ID emitted by the engine. A full-width key avoids routing
// collisions between concurrent datagram streams that share the same 32-bit
// prefix.
func datagramChannelIDFromStreamID(streamID string) (datagramChannelID, error) {
	var id datagramChannelID
	streamID = strings.ReplaceAll(strings.TrimSpace(streamID), "-", "")
	if len(streamID) < datagramChannelIDSize*2 {
		return id, fmt.Errorf("stream ID %q is too short for QUIC datagram channel routing", streamID)
	}
	for i := 0; i < datagramChannelIDSize; i++ {
		hi, ok := hexNibble(streamID[i*2])
		if !ok {
			return id, fmt.Errorf("stream ID %q contains non-hex characters", streamID)
		}
		lo, ok := hexNibble(streamID[i*2+1])
		if !ok {
			return id, fmt.Errorf("stream ID %q contains non-hex characters", streamID)
		}
		id[i] = (hi << 4) | lo
	}
	return id, nil
}

func (id datagramChannelID) String() string {
	return fmt.Sprintf("%x", id[:])
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// datagramDeadline tracks one read or write deadline for a datagram channel.
// wait() returns a channel closed once the deadline expires; setting a new
// deadline replaces the channel so pending waiters only observe the deadline
// that was active when they started waiting.
type datagramDeadline struct {
	mu    sync.Mutex
	timer *time.Timer
	ch    chan struct{}
}

func newDatagramDeadline() *datagramDeadline {
	return &datagramDeadline{ch: make(chan struct{})}
}

func (d *datagramDeadline) set(t time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	select {
	case <-d.ch:
		d.ch = make(chan struct{})
	default:
	}
	if t.IsZero() {
		return
	}
	if dur := time.Until(t); dur <= 0 {
		close(d.ch)
	} else {
		ch := d.ch
		d.timer = time.AfterFunc(dur, func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.ch == ch {
				close(ch)
			}
		})
	}
}

func (d *datagramDeadline) wait() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ch
}

func (d *datagramDeadline) expired() bool {
	select {
	case <-d.wait():
		return true
	default:
		return false
	}
}

// quicDatagramChannel implements net.PacketConn for a single datagram tunnel
// channel identified by a full stream-derived channel ID. Datagrams sent on
// WriteTo are prefixed with the channel ID; incoming datagrams are received on
// recvCh after the datagramReadLoop strips the channel ID prefix and routes them
// here.
type quicDatagramChannel struct {
	channelID     datagramChannelID
	provider      DatagramProvider
	laddr         net.Addr
	raddr         net.Addr
	recvCh        chan []byte
	ctx           context.Context
	cancel        context.CancelFunc
	once          sync.Once
	onClose       func(*quicDatagramChannel)
	readDeadline  *datagramDeadline
	writeDeadline *datagramDeadline
}

func (c *quicDatagramChannel) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data, ok := <-c.recvCh:
		if !ok {
			return 0, nil, net.ErrClosed
		}
		n := copy(p, data)
		return n, c.raddr, nil
	case <-c.ctx.Done():
		return 0, nil, net.ErrClosed
	case <-c.readDeadline.wait():
		return 0, nil, os.ErrDeadlineExceeded
	}
}

func (c *quicDatagramChannel) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.writeDeadline.expired() {
		return 0, os.ErrDeadlineExceeded
	}
	buf := make([]byte, datagramChannelIDSize+len(p))
	copy(buf[:datagramChannelIDSize], c.channelID[:])
	copy(buf[datagramChannelIDSize:], p)
	if err := c.provider.SendDatagram(buf); err != nil {
		var tooLarge *quic.DatagramTooLargeError
		if errors.As(err, &tooLarge) {
			return 0, fmt.Errorf("%w: %d bytes (max %d)", ErrDatagramTooLarge, len(p), tooLarge.MaxDatagramPayloadSize-datagramChannelIDSize)
		}
		return 0, err
	}
	return len(p), nil
}

func (c *quicDatagramChannel) Close() error {
	c.once.Do(func() {
		c.cancel()
		if c.onClose != nil {
			c.onClose(c)
		}
	})
	return nil
}

func (c *quicDatagramChannel) LocalAddr() net.Addr {
	return c.laddr
}

func (c *quicDatagramChannel) SetDeadline(t time.Time) error {
	c.readDeadline.set(t)
	c.writeDeadline.set(t)
	return nil
}

func (c *quicDatagramChannel) SetReadDeadline(t time.Time) error {
	c.readDeadline.set(t)
	return nil
}

func (c *quicDatagramChannel) SetWriteDeadline(t time.Time) error {
	c.writeDeadline.set(t)
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
		if ch, ok := conn.(*quicDatagramChannel); ok {
			return conn, ch.raddr, nil
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
