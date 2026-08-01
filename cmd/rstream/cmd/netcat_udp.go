// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
)

// runNetcatUDPUpstreamSession bridges one datagram tunnel session and a
// connected UDP socket toward a local service. One UDP packet equals one
// tunnel datagram, with no framing involved.
func runNetcatUDPUpstreamSession(ctx context.Context, conn net.PacketConn, raddr net.Addr, target string, idleTimeout time.Duration, logger *slog.Logger) error {
	udpAddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to resolve udp target %s: %w", target, err)
	}
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to dial udp target %s: %w", target, err)
	}
	defer udpConn.Close()
	defer conn.Close()
	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
			_ = udpConn.Close()
		case <-doneCh:
		}
	}()
	errCh := make(chan error, 2)
	go func() {
		buf := make([]byte, maxNetcatFrameSize)
		for {
			if idleTimeout > 0 {
				if err := conn.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
					errCh <- err
					return
				}
			}
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				if errors.Is(err, os.ErrDeadlineExceeded) {
					logger.Info("netcat udp session idle timeout reached")
					errCh <- nil
					return
				}
				errCh <- normalizeNetcatCopyError(err)
				return
			}
			if _, err := udpConn.Write(buf[:n]); err != nil {
				errCh <- normalizeNetcatCopyError(err)
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, maxNetcatFrameSize)
		for {
			n, err := udpConn.Read(buf)
			if err != nil {
				errCh <- normalizeNetcatCopyError(err)
				return
			}
			if _, err := conn.WriteTo(buf[:n], raddr); err != nil {
				if errors.Is(err, rstream.ErrDatagramTooLarge) {
					logger.Warn("dropping datagram exceeding transport limit", "size", n, "error", err)
					continue
				}
				errCh <- normalizeNetcatCopyError(err)
				return
			}
		}
	}()
	err = <-errCh
	_ = conn.Close()
	_ = udpConn.Close()
	if err != nil {
		return err
	}
	return ctx.Err()
}

type netcatUDPBridgeSession struct {
	peer      *net.UDPAddr
	packets   chan []byte
	ready     chan struct{}
	done      chan struct{}
	readyOnce sync.Once
	mu        sync.Mutex
	conn      net.PacketConn
	raddr     net.Addr
	openErr   error
	closed    bool
}

const netcatUDPBridgeQueueSize = 64

func newNetcatUDPBridgeSession(peer *net.UDPAddr) *netcatUDPBridgeSession {
	return &netcatUDPBridgeSession{peer: peer, packets: make(chan []byte, netcatUDPBridgeQueueSize), ready: make(chan struct{}), done: make(chan struct{})}
}

func (s *netcatUDPBridgeSession) resolveReady(err error) {
	s.readyOnce.Do(func() {
		s.mu.Lock()
		s.openErr = err
		s.mu.Unlock()
		close(s.ready)
	})
}

func (s *netcatUDPBridgeSession) waitReady(ctx context.Context) error {
	select {
	case <-s.ready:
		s.mu.Lock()
		err := s.openErr
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *netcatUDPBridgeSession) attach(conn net.PacketConn, raddr net.Addr) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return false
	}
	s.conn = conn
	s.raddr = raddr
	s.mu.Unlock()
	return true
}

func (s *netcatUDPBridgeSession) enqueue(packet []byte) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	payload := append([]byte(nil), packet...)
	select {
	case s.packets <- payload:
		return true
	case <-s.done:
		return false
	default:
		return false
	}
}

func (s *netcatUDPBridgeSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	close(s.done)
	s.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func runNetcatUDPBridgeSession(ctx context.Context, cfg *netcatServerConfig, udpConn *net.UDPConn, session *netcatUDPBridgeSession) {
	logger := cfg.Logger.With("peer", session.peer.String())
	dialCtx, cancelDial := context.WithTimeout(ctx, cfg.OpenTimeout)
	conn, raddr, err := cfg.PacketDial(dialCtx)
	cancelDial()
	if err != nil {
		session.resolveReady(err)
		if ctx.Err() == nil {
			logger.Warn("netcat udp bridge failed to dial tunnel", "error", err)
		}
		return
	}
	if !session.attach(conn, raddr) {
		session.resolveReady(net.ErrClosed)
		return
	}
	session.resolveReady(nil)
	logger.Debug("netcat udp bridge session opened")
	var readWG sync.WaitGroup
	readErrCh := make(chan error, 1)
	readWG.Add(1)
	go func() {
		defer readWG.Done()
		buf := make([]byte, maxNetcatFrameSize)
		for {
			if cfg.IdleTimeout > 0 {
				if err := conn.SetReadDeadline(time.Now().Add(cfg.IdleTimeout)); err != nil {
					readErrCh <- err
					return
				}
			}
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				readErrCh <- err
				return
			}
			if _, err := udpConn.WriteToUDP(buf[:n], session.peer); err != nil {
				readErrCh <- err
				return
			}
		}
	}()
	defer func() {
		_ = session.Close()
		readWG.Wait()
		logger.Debug("netcat udp bridge session closed")
	}()
	for {
		select {
		case packet := <-session.packets:
			if _, err := conn.WriteTo(packet, raddr); err != nil {
				if errors.Is(err, rstream.ErrDatagramTooLarge) {
					logger.Warn("dropping datagram exceeding transport limit", "size", len(packet), "error", err)
					continue
				}
				if ctx.Err() == nil {
					logger.Warn("netcat udp bridge failed to write tunnel datagram", "error", err)
				}
				return
			}
		case err := <-readErrCh:
			if errors.Is(err, os.ErrDeadlineExceeded) {
				logger.Info("netcat udp session idle timeout reached")
			} else if err != nil && ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				logger.Warn("netcat udp bridge failed to read tunnel datagram", "error", err)
			}
			return
		case <-ctx.Done():
			return
		case <-session.done:
			return
		}
	}
}

// runNetcatUDPListenerBridge binds a local UDP socket and bridges it to a
// datagram tunnel. Each local peer address maps to its own tunnel session,
// dialed on the peer's first packet, or eagerly for the configured UDPPeer so
// receive-only applications get traffic without sending first.
func runNetcatUDPListenerBridge(ctx context.Context, cfg *netcatServerConfig) error {
	laddr, err := net.ResolveUDPAddr("udp", cfg.UDPListen)
	if err != nil {
		return fmt.Errorf("failed to resolve udp listen address %s: %w", cfg.UDPListen, err)
	}
	udpConn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("failed to listen on udp %s: %w", cfg.UDPListen, err)
	}
	return runNetcatUDPBridgeLoop(ctx, cfg, udpConn)
}

func runNetcatUDPBridgeLoop(ctx context.Context, cfg *netcatServerConfig, udpConn *net.UDPConn) error {
	defer udpConn.Close()
	cfg.Logger.Info("netcat udp bridge started", "listen", udpConn.LocalAddr().String())
	sessionCtx, cancelSessions := context.WithCancel(ctx)
	stopListener := context.AfterFunc(sessionCtx, func() {
		_ = udpConn.Close()
	})
	var wg sync.WaitGroup
	var mu sync.Mutex
	sessions := make(map[string]*netcatUDPBridgeSession)
	closeSessions := func() {
		mu.Lock()
		snapshot := make([]*netcatUDPBridgeSession, 0, len(sessions))
		for _, session := range sessions {
			snapshot = append(snapshot, session)
		}
		mu.Unlock()
		for _, session := range snapshot {
			_ = session.Close()
		}
	}
	defer func() {
		cancelSessions()
		stopListener()
		closeSessions()
		wg.Wait()
	}()
	startSession := func(peer *net.UDPAddr) *netcatUDPBridgeSession {
		key := peer.String()
		mu.Lock()
		if existing := sessions[key]; existing != nil {
			mu.Unlock()
			return existing
		}
		if cfg.MaxConnections > 0 && len(sessions) >= cfg.MaxConnections {
			mu.Unlock()
			cfg.Logger.Warn("netcat udp bridge connection limit reached", "limit", cfg.MaxConnections, "peer", key)
			return nil
		}
		peerCopy := *peer
		session := newNetcatUDPBridgeSession(&peerCopy)
		sessions[key] = session
		wg.Add(1)
		mu.Unlock()
		go func() {
			defer wg.Done()
			runNetcatUDPBridgeSession(sessionCtx, cfg, udpConn, session)
			mu.Lock()
			if sessions[key] == session {
				delete(sessions, key)
			}
			mu.Unlock()
		}()
		return session
	}
	if cfg.UDPPeer != "" {
		peer, err := net.ResolveUDPAddr("udp", cfg.UDPPeer)
		if err != nil {
			cancelSessions()
			return fmt.Errorf("failed to resolve --udp-peer %s: %w", cfg.UDPPeer, err)
		}
		session := startSession(peer)
		if session == nil && ctx.Err() == nil {
			return fmt.Errorf("failed to open tunnel session for --udp-peer %s", cfg.UDPPeer)
		}
		if session != nil {
			if err := session.waitReady(ctx); err != nil && ctx.Err() == nil {
				return fmt.Errorf("failed to open tunnel session for --udp-peer %s: %w", cfg.UDPPeer, err)
			}
		}
	}
	buf := make([]byte, maxNetcatFrameSize)
	for {
		n, peer, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			return err
		}
		session := startSession(peer)
		if session == nil {
			continue
		}
		if !session.enqueue(buf[:n]) {
			cfg.Logger.Debug("dropping datagram while tunnel session queue is full", "size", n, "peer", peer.String())
		}
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
