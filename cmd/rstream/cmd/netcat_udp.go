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
	conn  net.PacketConn
	raddr net.Addr
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
	sessionCtx, cancelSessions := context.WithCancel(context.Background())
	defer cancelSessions()
	go func() {
		select {
		case <-ctx.Done():
			cancelSessions()
			_ = udpConn.Close()
		case <-sessionCtx.Done():
		}
	}()
	var wg sync.WaitGroup
	var mu sync.Mutex
	sessions := make(map[string]*netcatUDPBridgeSession)
	closeSessions := func() {
		mu.Lock()
		for _, session := range sessions {
			_ = session.conn.Close()
		}
		mu.Unlock()
	}
	openSession := func(peer *net.UDPAddr) *netcatUDPBridgeSession {
		mu.Lock()
		if existing := sessions[peer.String()]; existing != nil {
			mu.Unlock()
			return existing
		}
		if cfg.MaxConnections > 0 && len(sessions) >= cfg.MaxConnections {
			mu.Unlock()
			cfg.Logger.Warn("netcat udp bridge connection limit reached", "limit", cfg.MaxConnections, "peer", peer.String())
			return nil
		}
		mu.Unlock()
		dialCtx, cancelDial := context.WithTimeout(sessionCtx, cfg.OpenTimeout)
		conn, raddr, err := cfg.PacketDial(dialCtx)
		cancelDial()
		if err != nil {
			cfg.Logger.Warn("netcat udp bridge failed to dial tunnel", "peer", peer.String(), "error", err)
			return nil
		}
		session := &netcatUDPBridgeSession{conn: conn, raddr: raddr}
		mu.Lock()
		sessions[peer.String()] = session
		mu.Unlock()
		logger := cfg.Logger.With("peer", peer.String())
		logger.Debug("netcat udp bridge session opened")
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				mu.Lock()
				delete(sessions, peer.String())
				mu.Unlock()
				_ = conn.Close()
				logger.Debug("netcat udp bridge session closed")
			}()
			buf := make([]byte, maxNetcatFrameSize)
			for {
				if cfg.IdleTimeout > 0 {
					if err := conn.SetReadDeadline(time.Now().Add(cfg.IdleTimeout)); err != nil {
						return
					}
				}
				n, _, err := conn.ReadFrom(buf)
				if err != nil {
					if errors.Is(err, os.ErrDeadlineExceeded) {
						logger.Info("netcat udp session idle timeout reached")
					}
					return
				}
				if _, err := udpConn.WriteToUDP(buf[:n], peer); err != nil {
					return
				}
			}
		}()
		return session
	}
	if cfg.UDPPeer != "" {
		peer, err := net.ResolveUDPAddr("udp", cfg.UDPPeer)
		if err != nil {
			cancelSessions()
			return fmt.Errorf("failed to resolve --udp-peer %s: %w", cfg.UDPPeer, err)
		}
		if openSession(peer) == nil && ctx.Err() == nil {
			cancelSessions()
			closeSessions()
			wg.Wait()
			return fmt.Errorf("failed to open tunnel session for --udp-peer %s", cfg.UDPPeer)
		}
	}
	buf := make([]byte, maxNetcatFrameSize)
	for {
		n, peer, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			cancelSessions()
			closeSessions()
			wg.Wait()
			return err
		}
		session := openSession(peer)
		if session == nil {
			continue
		}
		if _, err := session.conn.WriteTo(buf[:n], session.raddr); err != nil {
			if errors.Is(err, rstream.ErrDatagramTooLarge) {
				cfg.Logger.Warn("dropping datagram exceeding transport limit", "size", n, "peer", peer.String(), "error", err)
				continue
			}
			_ = session.conn.Close()
		}
	}
	cancelSessions()
	closeSessions()
	wg.Wait()
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
