// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/netretry"
)

const netcatAcceptRetryMaxDelay = time.Second

type netcatManagedListener struct {
	net.Listener
	ctrl io.Closer
	once sync.Once
	err  error
}

type netcatLockedWriter struct {
	w  io.Writer
	mu sync.Mutex
}

type netcatCopyResult struct {
	direction string
	err       error
}

func (w *netcatLockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (l *netcatManagedListener) Close() error {
	l.once.Do(func() {
		if l.Listener != nil {
			l.err = l.Listener.Close()
		}
		if l.ctrl != nil {
			if err := l.ctrl.Close(); l.err == nil {
				l.err = err
			}
		}
	})
	return l.err
}

func runNetcatServer(ctx context.Context, cfg *netcatServerConfig) error {
	if cfg == nil {
		return fmt.Errorf("server config is required")
	}
	if cfg.Listen == nil {
		return fmt.Errorf("listener factory is required")
	}
	if cfg.Upstream == nil && cfg.Exec == nil {
		return fmt.Errorf("server mode requires an upstream or exec configuration")
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = defaultNetcatOpenTimeout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	defer closeNetcatTransport(cfg.CloseTransport, cfg.Logger)
	result, err := cfg.Listen(ctx)
	if err != nil {
		return err
	}
	defer result.Listener.Close()
	cfg.Logger.Info("netcat server started", "listen", result.Display)
	if result.Generated {
		fmt.Fprintln(cfg.Stderr, result.Display)
	}
	sessionCtx, cancelSessions := context.WithCancel(ctx)
	defer cancelSessions()
	stopListener := context.AfterFunc(ctx, func() { _ = result.Listener.Close() })
	defer stopListener()
	var wg sync.WaitGroup
	var slots chan struct{}
	var acceptRetryDelay time.Duration
	if cfg.MaxConnections > 0 {
		slots = make(chan struct{}, cfg.MaxConnections)
	}
	for {
		conn, err := result.Listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			delay, retry := netretry.NextAcceptDelay(err, acceptRetryDelay, netcatAcceptRetryMaxDelay)
			if retry && netretry.Wait(ctx, delay) {
				acceptRetryDelay = delay
				continue
			}
			cancelSessions()
			wg.Wait()
			return err
		}
		acceptRetryDelay = 0
		acquiredSlot := false
		if slots != nil {
			select {
			case slots <- struct{}{}:
				acquiredSlot = true
			default:
				cfg.Logger.Warn("netcat server connection limit reached", "limit", cfg.MaxConnections, "peer", conn.RemoteAddr().String())
				_ = conn.Close()
				continue
			}
		}
		wg.Add(1)
		go func(inbound net.Conn) {
			defer wg.Done()
			if acquiredSlot {
				defer func() { <-slots }()
			}
			logger := cfg.Logger.With("peer", inbound.RemoteAddr().String())
			var err error
			switch {
			case cfg.Upstream != nil:
				err = runNetcatProxySession(sessionCtx, inbound, cfg.Upstream, cfg.OpenTimeout, cfg.DownstreamHalfClose, cfg.UpstreamHalfClose, logger)
			case cfg.Exec != nil:
				err = runNetcatExecSession(sessionCtx, inbound, cfg.Exec, cfg.DownstreamHalfClose, logger)
			}
			if err != nil && sessionCtx.Err() == nil {
				logger.Warn("netcat session failed", "error", err)
			}
		}(conn)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runNetcatProxySession(ctx context.Context, downstream net.Conn, dialUpstream netcatDialer, openTimeout time.Duration, downstreamHalfClose bool, upstreamHalfClose bool, logger *slog.Logger) error {
	defer downstream.Close()
	dialCtx, cancelDial := context.WithTimeout(ctx, openTimeout)
	defer cancelDial()
	upstream, err := dialUpstream(dialCtx)
	if err != nil {
		return err
	}
	defer upstream.Close()
	logger.Debug("netcat proxy session connected", "upstream", upstream.RemoteAddr().String())
	doneCh := make(chan struct{})
	watcherDone := make(chan struct{})
	defer func() {
		close(doneCh)
		<-watcherDone
	}()
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = downstream.Close()
			_ = upstream.Close()
		case <-doneCh:
		}
	}()
	closeDownstream := sync.OnceFunc(func() {
		_ = downstream.Close()
	})
	errCh := make(chan netcatCopyResult, 2)
	go func() {
		n, err := copyNetcatStream(upstream, downstream, upstreamHalfClose)
		logger.Debug("netcat proxy copy completed", "direction", "downstream_to_upstream", "bytes", n, "error", err)
		errCh <- netcatCopyResult{direction: "downstream_to_upstream", err: err}
	}()
	go func() {
		n, err := copyNetcatStream(downstream, upstream, downstreamHalfClose)
		logger.Debug("netcat proxy copy completed", "direction", "upstream_to_downstream", "bytes", n, "error", err)
		errCh <- netcatCopyResult{direction: "upstream_to_downstream", err: err}
	}()
	var errs [2]error
	for i := 0; i < len(errs); i++ {
		result := <-errCh
		errs[i] = result.err
		if result.err != nil {
			closeDownstream()
			_ = upstream.Close()
		}
		if result.direction == "upstream_to_downstream" && !downstreamHalfClose {
			closeDownstream()
		}
	}
	if err := firstNetcatError(errs[:]...); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runNetcatExecSession(ctx context.Context, downstream net.Conn, execCfg *netcatExecConfig, downstreamHalfClose bool, logger *slog.Logger) error {
	defer downstream.Close()
	cmd, stdin, stdout, stderr, err := startNetcatCommand(execCfg, logger)
	if err != nil {
		return err
	}
	closeStdin := sync.OnceFunc(func() {
		if stdin != nil {
			_ = stdin.Close()
		}
	})
	closeDownstream := sync.OnceFunc(func() {
		_ = downstream.Close()
	})
	doneCh := make(chan struct{})
	watcherDone := make(chan struct{})
	defer func() {
		close(doneCh)
		<-watcherDone
	}()
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			closeStdin()
			closeDownstream()
			killNetcatProcess(cmd)
		case <-doneCh:
		}
	}()
	childErrCh := make(chan error, 1)
	stdinErrCh := make(chan error, 1)
	stdoutErrCh := make(chan error, 1)
	stderrErrCh := make(chan error, 1)
	writer := &netcatLockedWriter{w: downstream}
	go func() {
		n, err := copyNetcatCommandInput(stdin, downstream, closeStdin)
		logger.Debug("netcat exec input completed", "bytes", n, "error", err)
		stdinErrCh <- err
	}()
	go func() {
		n, err := io.Copy(writer, stdout)
		logger.Debug("netcat exec stdout completed", "bytes", n, "error", err)
		stdoutErrCh <- normalizeNetcatCopyError(err)
	}()
	go func() {
		n, err := io.Copy(writer, stderr)
		logger.Debug("netcat exec stderr completed", "bytes", n, "error", err)
		stderrErrCh <- normalizeNetcatCopyError(err)
	}()
	stdoutDone := false
	stderrDone := false
	stdinDone := false
	childDone := false
	childWaitStarted := false
	var stdoutErr error
	var stderrErr error
	var stdinErr error
	var childErr error
	for !stdoutDone || !stderrDone || !stdinDone || !childDone {
		select {
		case err := <-stdoutErrCh:
			stdoutDone = true
			stdoutErr = err
			if err != nil {
				closeDownstream()
				closeStdin()
				killNetcatProcess(cmd)
			}
		case err := <-stderrErrCh:
			stderrDone = true
			stderrErr = err
			if err != nil {
				closeDownstream()
				closeStdin()
				killNetcatProcess(cmd)
			}
		case err := <-stdinErrCh:
			stdinDone = true
			stdinErr = err
			if err != nil || !downstreamHalfClose {
				closeDownstream()
				closeStdin()
				killNetcatProcess(cmd)
			}
		case err := <-childErrCh:
			childDone = true
			childErr = err
			logger.Debug("netcat child completed", "error", err)
			closeStdin()
		case <-ctx.Done():
			closeDownstream()
			closeStdin()
			killNetcatProcess(cmd)
		}
		if !downstreamHalfClose && childDone && stdoutDone && stderrDone {
			closeDownstream()
		}
		if downstreamHalfClose && childDone && stdoutDone && stderrDone && !stdinDone {
			closeDownstream()
		}
		if stdoutDone && stderrDone && !childWaitStarted {
			childWaitStarted = true
			go func() {
				childErrCh <- waitNetcatProcess(cmd)
			}()
		}
	}
	if err := firstNetcatError(stdoutErr, stderrErr, stdinErr, childErr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func copyNetcatStream(dst net.Conn, src net.Conn, halfClose bool) (int64, error) {
	n, err := io.Copy(dst, src)
	if err != nil {
		return n, normalizeNetcatCopyError(err)
	}
	if halfClose {
		if err := closeNetcatWrite(dst); err != nil {
			return n, err
		}
	}
	_ = closeNetcatRead(src)
	return n, nil
}

func copyNetcatCommandInput(stdin io.WriteCloser, downstream net.Conn, closeStdin func()) (int64, error) {
	defer closeStdin()
	if stdin == nil {
		return 0, nil
	}
	n, err := io.Copy(stdin, downstream)
	if err != nil {
		return n, normalizeNetcatCommandInputError(err)
	}
	return n, nil
}

func startNetcatCommand(cfg *netcatExecConfig, logger *slog.Logger) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	if cfg == nil {
		return nil, nil, nil, nil, fmt.Errorf("exec config is required")
	}
	if cfg.Command == "" {
		return nil, nil, nil, nil, fmt.Errorf("command is required")
	}
	var cmd *exec.Cmd
	if cfg.Shell {
		shell, args := netcatShellCommand(cfg.Command)
		logger.Debug("starting shell command", "shell", shell, "command", cfg.Command)
		cmd = exec.Command(shell, args...)
	} else {
		args := splitNetcatCommand(cfg.Command)
		if len(args) == 0 {
			return nil, nil, nil, nil, fmt.Errorf("command is required")
		}
		logger.Debug("starting command", "exe", args[0], "args", args[1:])
		cmd = exec.Command(args[0], args[1:]...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, err
	}
	return cmd, stdin, stdout, stderr, nil
}

func waitNetcatProcess(cmd *exec.Cmd) error {
	if cmd == nil {
		return nil
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return normalizeNetcatCopyError(err)
}

func killNetcatProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
	}
}

func normalizeNetcatCopyError(err error) error {
	if err == nil || isNetcatClosedError(err) {
		return nil
	}
	return err
}

func normalizeNetcatCommandInputError(err error) error {
	if err == nil || isNetcatClosedError(err) || errors.Is(err, syscall.EPIPE) {
		return nil
	}
	return err
}

func closeNetcatWrite(conn net.Conn) error {
	type closeWriter interface {
		CloseWrite() error
	}
	if conn == nil {
		return nil
	}
	if cw, ok := conn.(closeWriter); ok {
		if err := cw.CloseWrite(); err != nil && !isNetcatClosedError(err) {
			return err
		}
	}
	return nil
}

func closeNetcatRead(conn net.Conn) error {
	type closeReader interface {
		CloseRead() error
	}
	if conn == nil {
		return nil
	}
	if cr, ok := conn.(closeReader); ok {
		if err := cr.CloseRead(); err != nil && !isNetcatClosedError(err) {
			return err
		}
	}
	return nil
}

func isNetcatClosedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return true
	}
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

func firstNetcatError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
