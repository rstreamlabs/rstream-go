// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
	"github.com/yosida95/uritemplate/v3"
)

const (
	masqueConnectUDPProtocol     = "connect-udp"
	masqueCapsuleProtocolHeader  = "?1"
	masqueHTTPDatagramContextID0 = byte(0)
)

type masqueUDPStream interface {
	io.ReadWriteCloser
	ReceiveDatagram(context.Context) ([]byte, error)
	SendDatagram([]byte) error
	CancelRead(quic.StreamErrorCode)
}

type masqueProxySession struct {
	conn      *quic.Conn
	transport *quic.Transport
	packet    net.PacketConn
}

func (s *masqueProxySession) Close() error {
	var err error
	if s.conn != nil {
		err = s.conn.CloseWithError(0, "MASQUE proxy session closed")
	}
	if closeErr := closeQUICTransportResources(s.transport, s.packet, nil); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

func dialMASQUEUDPProxy(ctx context.Context, tpl *uritemplate.Template, tlsCfg *tls.Config, network string, localAddr *net.UDPAddr, target string, remoteAddr net.Addr, dnsOpts dnsResolverConfig) (net.PacketConn, io.Closer, error) {
	expanded, err := expandMASQUEUDPTemplate(tpl, target)
	if err != nil {
		return nil, nil, err
	}
	proxyURL, err := url.Parse(expanded)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse expanded MASQUE proxy URL: %w", err)
	}
	proxyDialAddr := proxyURL.Host
	if dnsOpts.enabled() {
		proxyDialAddr, err = resolveDialAddress(ctx, proxyDialAddr, dnsOpts)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve MASQUE proxy address: %w", err)
		}
	}
	packet, proxyAddr, err := dialMASQUEProxyPacketConn(network, localAddr, proxyDialAddr)
	if err != nil {
		return nil, nil, err
	}
	qtransport := &quic.Transport{Conn: packet}
	quicCfg := &quic.Config{
		EnableDatagrams:   true,
		InitialPacketSize: masqueProxyInitialPacketSize,
	}
	conn, err := qtransport.Dial(ctx, proxyAddr, tlsCfg, quicCfg)
	if err != nil {
		_ = closeQUICTransportResources(qtransport, packet, nil)
		return nil, nil, fmt.Errorf("failed to dial MASQUE proxy: %w", err)
	}
	session := &masqueProxySession{
		conn:      conn,
		transport: qtransport,
		packet:    packet,
	}
	proxiedConn, err := openMASQUEUDPStream(ctx, conn, proxyURL, remoteAddr)
	if err != nil {
		_ = session.Close()
		return nil, nil, err
	}
	return proxiedConn, session, nil
}

func dialMASQUEProxyPacketConn(network string, localAddr *net.UDPAddr, proxyDialAddr string) (net.PacketConn, net.Addr, error) {
	packet, err := net.ListenUDP(network, localAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create UDP socket for MASQUE proxy: %w", err)
	}
	proxyAddr, err := net.ResolveUDPAddr(network, proxyDialAddr)
	if err != nil {
		_ = packet.Close()
		return nil, nil, fmt.Errorf("failed to resolve MASQUE proxy UDP address %q: %w", proxyDialAddr, err)
	}
	return packet, proxyAddr, nil
}

func expandMASQUEUDPTemplate(tpl *uritemplate.Template, target string) (string, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return "", fmt.Errorf("failed to parse MASQUE target address: %w", err)
	}
	expanded, err := tpl.Expand(uritemplate.Values{
		"target_host": uritemplate.String(escapeMASQUEHost(host)),
		"target_port": uritemplate.String(port),
	})
	if err != nil {
		return "", fmt.Errorf("failed to expand MASQUE URI template: %w", err)
	}
	return expanded, nil
}

func escapeMASQUEHost(host string) string {
	return strings.ReplaceAll(host, ":", "%3A")
}

func openMASQUEUDPStream(ctx context.Context, conn *quic.Conn, proxyURL *url.URL, remoteAddr net.Addr) (net.PacketConn, error) {
	http3Transport := &http3.Transport{EnableDatagrams: true}
	clientConn := http3Transport.NewClientConn(conn)
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-clientConn.Context().Done():
		return nil, context.Cause(clientConn.Context())
	case <-clientConn.ReceivedSettings():
	}
	settings := clientConn.Settings()
	if !settings.EnableExtendedConnect {
		return nil, errors.New("MASQUE proxy did not enable Extended CONNECT")
	}
	if !settings.EnableDatagrams {
		return nil, errors.New("MASQUE proxy did not enable HTTP datagrams")
	}
	stream, err := clientConn.OpenRequestStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open MASQUE CONNECT-UDP request stream: %w", err)
	}
	req := &http.Request{
		Method: http.MethodConnect,
		Proto:  masqueConnectUDPProtocol,
		Host:   proxyURL.Host,
		Header: http.Header{http3.CapsuleProtocolHeader: []string{masqueCapsuleProtocolHeader}},
		URL:    proxyURL,
	}
	if err := stream.SendRequestHeader(req); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("failed to send MASQUE CONNECT-UDP request: %w", err)
	}
	resp, err := stream.ReadResponse()
	if err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("failed to read MASQUE CONNECT-UDP response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode > 299 {
		_ = stream.Close()
		return nil, fmt.Errorf("MASQUE CONNECT-UDP failed with %s", resp.Status)
	}
	return newMASQUEUDPPacketConn(stream, &masqueAddr{conn.LocalAddr()}, remoteAddr), nil
}

type masqueAddr struct {
	net.Addr
}

func (a *masqueAddr) Network() string {
	return "connect-udp"
}

type masqueUDPPacketConn struct {
	stream     masqueUDPStream
	localAddr  net.Addr
	remoteAddr net.Addr
	closed     atomic.Bool
	closeOnce  sync.Once
	closeErr   error
	readDone   chan struct{}

	deadlineMu        sync.Mutex
	readCtx           context.Context
	readCancel        context.CancelFunc
	readDeadline      time.Time
	readDeadlineTimer *time.Timer
	writeDeadline     *packetDeadline
}

var _ net.PacketConn = (*masqueUDPPacketConn)(nil)

func newMASQUEUDPPacketConn(stream masqueUDPStream, localAddr net.Addr, remoteAddr net.Addr) *masqueUDPPacketConn {
	ctx, cancel := context.WithCancel(context.Background())
	conn := &masqueUDPPacketConn{
		stream:        stream,
		localAddr:     localAddr,
		remoteAddr:    remoteAddr,
		readDone:      make(chan struct{}),
		readCtx:       ctx,
		readCancel:    cancel,
		writeDeadline: newPacketDeadline(),
	}
	go conn.discardCapsules()
	return conn
}

func (c *masqueUDPPacketConn) discardCapsules() {
	defer close(c.readDone)
	_ = discardMASQUECapsules(quicvarint.NewReader(c.stream))
	_ = c.stream.Close()
}

func (c *masqueUDPPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		ctx := c.currentReadContext()
		data, err := c.stream.ReceiveDatagram(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				return 0, nil, err
			}
			if c.closed.Load() {
				return 0, nil, net.ErrClosed
			}
			if c.shouldRestartExpiredRead() {
				continue
			}
			return 0, nil, os.ErrDeadlineExceeded
		}
		contextID, n, err := quicvarint.Parse(data)
		if err != nil {
			return 0, nil, fmt.Errorf("malformed MASQUE HTTP datagram: %w", err)
		}
		if contextID != 0 {
			continue
		}
		return copy(b, data[n:]), c.remoteAddr, nil
	}
}

func (c *masqueUDPPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	if addr == nil || c.remoteAddr == nil || addr.String() != c.remoteAddr.String() {
		return 0, fmt.Errorf("invalid remote address %v; expected %v", addr, c.remoteAddr)
	}
	if c.writeDeadline.expired() {
		return 0, os.ErrDeadlineExceeded
	}
	data := make([]byte, 0, 1+len(p))
	data = append(data, masqueHTTPDatagramContextID0)
	data = append(data, p...)
	if err := c.stream.SendDatagram(data); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *masqueUDPPacketConn) Close() error {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		c.stream.CancelRead(quic.StreamErrorCode(http3.ErrCodeNoError))
		c.closeErr = c.stream.Close()
		<-c.readDone
		c.readCancel()
		c.deadlineMu.Lock()
		if c.readDeadlineTimer != nil {
			c.readDeadlineTimer.Stop()
		}
		c.deadlineMu.Unlock()
	})
	return c.closeErr
}

func (c *masqueUDPPacketConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *masqueUDPPacketConn) SetDeadline(t time.Time) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeDeadline.set(t)
	return c.SetReadDeadline(t)
}

func (c *masqueUDPPacketConn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.readDeadline = t
	now := time.Now()
	if t.IsZero() {
		if c.readDeadlineTimer != nil {
			c.readDeadlineTimer.Stop()
		}
		if c.readContextDoneLocked() {
			c.resetReadContextLocked()
		}
		return nil
	}
	if !t.After(now) {
		if c.readDeadlineTimer != nil {
			c.readDeadlineTimer.Stop()
		}
		c.readCancel()
		return nil
	}
	deadline := t.Sub(now)
	if c.readDeadlineTimer != nil {
		if c.readContextDoneLocked() {
			c.resetReadContextLocked()
		}
		c.readDeadlineTimer.Reset(deadline)
		return nil
	}
	c.readDeadlineTimer = time.AfterFunc(deadline, func() {
		c.deadlineMu.Lock()
		defer c.deadlineMu.Unlock()
		if !c.readDeadline.IsZero() && c.readDeadline.Before(time.Now()) {
			c.readCancel()
		}
	})
	return nil
}

func (c *masqueUDPPacketConn) readContextDoneLocked() bool {
	select {
	case <-c.readCtx.Done():
		return true
	default:
		return false
	}
}

func (c *masqueUDPPacketConn) resetReadContextLocked() {
	c.readCancel()
	c.readCtx, c.readCancel = context.WithCancel(context.Background())
}

func (c *masqueUDPPacketConn) SetWriteDeadline(t time.Time) error {
	if c.closed.Load() {
		return net.ErrClosed
	}
	c.writeDeadline.set(t)
	return nil
}

func (c *masqueUDPPacketConn) SetReadBuffer(int) error {
	return nil
}

func (c *masqueUDPPacketConn) SetWriteBuffer(int) error {
	return nil
}

func (c *masqueUDPPacketConn) currentReadContext() context.Context {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return c.readCtx
}

func (c *masqueUDPPacketConn) shouldRestartExpiredRead() bool {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	return time.Now().Before(c.readDeadline)
}

func discardMASQUECapsules(reader quicvarint.Reader) error {
	for {
		_, capsule, err := http3.ParseCapsule(reader)
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, capsule); err != nil {
			return err
		}
	}
}
