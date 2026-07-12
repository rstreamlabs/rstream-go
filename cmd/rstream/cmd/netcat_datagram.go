// See LICENSE file in the project root for license information.

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	rstream "github.com/rstreamlabs/rstream-go"
)

type netcatFraming string

const (
	// netcatFramingRFC4571 frames each datagram with a 2-byte big-endian
	// length prefix on stdio, as defined by RFC 4571.
	netcatFramingRFC4571 netcatFraming = "rfc4571"
	maxNetcatFrameSize                 = 65535
)

type netcatManagedPacketListener struct {
	rstream.PacketListener
	ctrl io.Closer
	once sync.Once
	err  error
}

func (l *netcatManagedPacketListener) Close() error {
	l.once.Do(func() {
		if l.PacketListener != nil {
			l.err = l.PacketListener.Close()
		}
		if l.ctrl != nil {
			if err := l.ctrl.Close(); l.err == nil {
				l.err = err
			}
		}
	})
	return l.err
}

func readNetcatFrame(r *bufio.Reader, buf []byte) (int, error) {
	header := buf[:2]
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, err
	}
	length := int(binary.BigEndian.Uint16(header))
	if _, err := io.ReadFull(r, buf[:length]); err != nil {
		return 0, err
	}
	return length, nil
}

func writeNetcatFrame(w *bufio.Writer, p []byte) error {
	if len(p) > maxNetcatFrameSize {
		return fmt.Errorf("datagram too large for rfc4571 framing: %d bytes", len(p))
	}
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(p)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if _, err := w.Write(p); err != nil {
		return err
	}
	return w.Flush()
}

// netcatDatagramSession bridges one datagram tunnel channel and a pair of
// framed byte streams. In carries RFC 4571 frames to send to the tunnel (nil
// disables sending); Out receives RFC 4571 frames read from the tunnel.
type netcatDatagramSession struct {
	Conn          net.PacketConn
	RemoteAddr    net.Addr
	In            io.Reader
	Out           io.Writer
	IdleTimeout   time.Duration
	EndOnInputEOF bool
	Logger        *slog.Logger
}

func runNetcatDatagramSession(ctx context.Context, s *netcatDatagramSession) error {
	defer s.Conn.Close()
	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Conn.Close()
		case <-doneCh:
		}
	}()
	outputErrCh := make(chan error, 1)
	go func() {
		err := netcatDatagramRecvLoop(s.Conn, s.Out, s.IdleTimeout, s.Logger)
		s.Logger.Debug("netcat datagram receive completed", "error", err)
		outputErrCh <- err
	}()
	var inputErrCh chan error
	if s.In != nil {
		inputErrCh = make(chan error, 1)
		go func() {
			err := netcatDatagramSendLoop(s.Conn, s.RemoteAddr, s.In, s.Logger)
			s.Logger.Debug("netcat datagram send completed", "error", err)
			inputErrCh <- err
		}()
	}
	for {
		select {
		case err := <-outputErrCh:
			_ = s.Conn.Close()
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		case err := <-inputErrCh:
			inputErrCh = nil
			if err != nil {
				_ = s.Conn.Close()
				return err
			}
			if s.EndOnInputEOF {
				_ = s.Conn.Close()
				return nil
			}
		case <-ctx.Done():
			_ = s.Conn.Close()
			return ctx.Err()
		}
	}
}

func netcatDatagramRecvLoop(conn net.PacketConn, out io.Writer, idleTimeout time.Duration, logger *slog.Logger) error {
	w := bufio.NewWriter(out)
	buf := make([]byte, maxNetcatFrameSize)
	for {
		if idleTimeout > 0 {
			if err := conn.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
				return err
			}
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				logger.Info("netcat datagram session idle timeout reached")
				return nil
			}
			return normalizeNetcatCopyError(err)
		}
		if err := writeNetcatFrame(w, buf[:n]); err != nil {
			return normalizeNetcatCommandInputError(err)
		}
	}
}

func netcatDatagramSendLoop(conn net.PacketConn, raddr net.Addr, in io.Reader, logger *slog.Logger) error {
	r := bufio.NewReader(in)
	buf := make([]byte, maxNetcatFrameSize)
	for {
		n, err := readNetcatFrame(r, buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("truncated rfc4571 frame on input: %w", err)
			}
			return normalizeNetcatCopyError(err)
		}
		if _, err := conn.WriteTo(buf[:n], raddr); err != nil {
			if errors.Is(err, rstream.ErrDatagramTooLarge) {
				logger.Warn("dropping datagram exceeding transport limit", "size", n, "error", err)
				continue
			}
			return normalizeNetcatCopyError(err)
		}
	}
}

// runNetcatDatagramExecSession bridges a datagram channel and a child process.
// The child reads received datagrams as RFC 4571 frames on stdin and writes
// RFC 4571 frames on stdout. Unlike bytestream exec sessions, the child's
// stderr goes to the local stderr instead of the connection, since interleaved
// raw stderr bytes would corrupt the framing.
func runNetcatDatagramExecSession(ctx context.Context, conn net.PacketConn, raddr net.Addr, execCfg *netcatExecConfig, idleTimeout time.Duration, stderr io.Writer, logger *slog.Logger) error {
	cmd, stdin, stdout, stderrPipe, err := startNetcatCommand(execCfg, logger)
	if err != nil {
		_ = conn.Close()
		return err
	}
	stderrDoneCh := make(chan struct{})
	go func() {
		_, _ = io.Copy(stderr, stderrPipe)
		close(stderrDoneCh)
	}()
	session := &netcatDatagramSession{
		Conn:          conn,
		RemoteAddr:    raddr,
		In:            stdout,
		Out:           stdin,
		IdleTimeout:   idleTimeout,
		EndOnInputEOF: true,
		Logger:        logger,
	}
	sessionErr := runNetcatDatagramSession(ctx, session)
	_ = stdin.Close()
	killNetcatProcess(cmd)
	<-stderrDoneCh
	waitErr := waitNetcatProcess(cmd)
	return firstNetcatError(sessionErr, waitErr)
}

func runNetcatDatagramClient(ctx context.Context, cfg *netcatClientConfig) error {
	if cfg == nil {
		return fmt.Errorf("client config is required")
	}
	if cfg.PacketDial == nil {
		return fmt.Errorf("client packet dialer is required")
	}
	if cfg.Stdin == nil {
		cfg.Stdin = bytes.NewReader(nil)
	}
	if cfg.Stdout == nil {
		cfg.Stdout = io.Discard
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	defer closeNetcatTransport(cfg.CloseTransport, cfg.Logger)
	conn, raddr, err := cfg.PacketDial(ctx)
	if err != nil {
		return err
	}
	cfg.Logger.Debug("netcat datagram client connected", "target", cfg.Target, "interactive", cfg.Interactive)
	if cfg.Exec != nil {
		return runNetcatDatagramExecSession(ctx, conn, raddr, cfg.Exec, cfg.IdleTimeout, cfg.Stderr, cfg.Logger)
	}
	var in io.Reader
	if cfg.Interactive {
		in = cfg.Stdin
	}
	session := &netcatDatagramSession{
		Conn:        conn,
		RemoteAddr:  raddr,
		In:          in,
		Out:         cfg.Stdout,
		IdleTimeout: cfg.IdleTimeout,
		Logger:      cfg.Logger,
	}
	return runNetcatDatagramSession(ctx, session)
}

func runNetcatDatagramServer(ctx context.Context, cfg *netcatServerConfig) error {
	if cfg == nil {
		return fmt.Errorf("server config is required")
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	defer closeNetcatTransport(cfg.CloseTransport, cfg.Logger)
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = defaultNetcatOpenTimeout
	}
	if cfg.UDPListen != "" {
		if cfg.PacketDial == nil {
			return fmt.Errorf("udp listener bridge requires a packet dialer")
		}
		return runNetcatUDPListenerBridge(ctx, cfg)
	}
	if cfg.PacketListen == nil {
		return fmt.Errorf("packet listener factory is required")
	}
	if cfg.Exec == nil && cfg.UpstreamUDP == "" {
		return fmt.Errorf("datagram server mode requires an exec or udp upstream configuration")
	}
	result, err := cfg.PacketListen(ctx)
	if err != nil {
		return err
	}
	defer result.Listener.Close()
	cfg.Logger.Info("netcat datagram server started", "listen", result.Display)
	if result.Generated {
		fmt.Fprintln(cfg.Stderr, result.Display)
	}
	sessionCtx, cancelSessions := context.WithCancel(context.Background())
	defer cancelSessions()
	go func() {
		select {
		case <-ctx.Done():
			cancelSessions()
			_ = result.Listener.Close()
		case <-sessionCtx.Done():
		}
	}()
	var wg sync.WaitGroup
	var slots chan struct{}
	if cfg.MaxConnections > 0 {
		slots = make(chan struct{}, cfg.MaxConnections)
	}
	for {
		conn, raddr, err := result.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			cancelSessions()
			wg.Wait()
			return err
		}
		acquiredSlot := false
		if slots != nil {
			select {
			case slots <- struct{}{}:
				acquiredSlot = true
			default:
				cfg.Logger.Warn("netcat server connection limit reached", "limit", cfg.MaxConnections, "peer", raddr.String())
				_ = conn.Close()
				continue
			}
		}
		wg.Add(1)
		go func(inbound net.PacketConn, peer net.Addr) {
			defer wg.Done()
			if acquiredSlot {
				defer func() { <-slots }()
			}
			logger := cfg.Logger.With("peer", peer.String())
			var err error
			if cfg.UpstreamUDP != "" {
				err = runNetcatUDPUpstreamSession(sessionCtx, inbound, peer, cfg.UpstreamUDP, cfg.IdleTimeout, logger)
			} else {
				err = runNetcatDatagramExecSession(sessionCtx, inbound, peer, cfg.Exec, cfg.IdleTimeout, cfg.Stderr, logger)
			}
			if err != nil && sessionCtx.Err() == nil {
				logger.Warn("netcat session failed", "error", err)
			}
		}(conn, raddr)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
