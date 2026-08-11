// See LICENSE file in the project root for license information.

package runengine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/netretry"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/streamrelay"
)

const acceptRetryMaxDelay = time.Second

type Runner struct {
	logger       *slog.Logger
	retryInitial time.Duration
	retryMax     time.Duration
}

type retryFailureLog struct {
	startedAt time.Time
	attempts  uint64
}

type Option func(*Runner)

func WithLogger(logger *slog.Logger) Option {
	return func(r *Runner) { r.logger = logger }
}

func WithRetry(initial, max time.Duration) Option {
	return func(r *Runner) {
		r.retryInitial = initial
		r.retryMax = max
	}
}

func New(options ...Option) *Runner {
	r := &Runner{
		logger:       slog.Default(),
		retryInitial: 1 * time.Second,
		retryMax:     30 * time.Second,
	}
	for _, opt := range options {
		opt(r)
	}
	return r
}

type handle struct {
	cancel context.CancelFunc
}

func (h *handle) Stop() error {
	if h == nil || h.cancel == nil {
		return nil
	}
	h.cancel()
	return nil
}

func (r *Runner) Start(ctx context.Context, desired runmodel.DesiredTunnel) (runmodel.Handle, error) {
	if r == nil {
		return &handle{}, nil
	}
	if desired.Name == "" {
		return nil, fmt.Errorf("tunnel name is required")
	}
	if strings.TrimSpace(desired.Context.Engine) == "" {
		return nil, fmt.Errorf("engine is required for tunnel %q", desired.Name)
	}
	if strings.TrimSpace(desired.Context.Token) == "" {
		return nil, fmt.Errorf("token is required for tunnel %q", desired.Name)
	}
	if err := rstream.MaybeSetGeneratedStableDomain(&desired.Props, desired.Context.StableDomainEndpoint()); err != nil {
		return nil, fmt.Errorf("failed to generate stable domain for tunnel %q: %w", desired.Name, err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	go r.run(workerCtx, desired)
	return &handle{cancel: cancel}, nil
}

func (r *Runner) run(ctx context.Context, desired runmodel.DesiredTunnel) {
	logger := r.logger.With(
		"tunnel", desired.Name,
		"forward", desired.Forward.String(),
		"source", desired.Source,
	)
	backoff := newBackoff(r.retryInitial, r.retryMax)
	failures := &retryFailureLog{}
	for {
		if ctx.Err() != nil {
			logger.Info("Tunnel stopped")
			return
		}
		err := r.runOnceReady(ctx, desired, logger, func() {
			backoff.Reset()
			failures.Recovered(logger)
		})
		if err == nil {
			logger.Info("Tunnel closed")
			return
		}
		if errors.Is(err, context.Canceled) {
			logger.Info("Tunnel stopped")
			return
		}
		if !retryableTunnelError(err) {
			logger.Error("Tunnel stopped", "error", err)
			return
		}
		retry := jitterRetry(backoff.Next())
		failures.Failed(logger, err, retry)
		if !netretry.Wait(ctx, retry) {
			logger.Info("Tunnel stopped")
			return
		}
	}
}

func (l *retryFailureLog) Failed(logger *slog.Logger, err error, retry time.Duration) {
	if l.attempts == 0 {
		l.startedAt = time.Now()
		logger.Warn("Tunnel unavailable; retrying", "error", err, "retry_in", retry)
	}
	l.attempts++
}

func (l *retryFailureLog) Recovered(logger *slog.Logger) {
	if l.attempts == 0 {
		return
	}
	logger.Info("Tunnel connection recovered", "failed_attempts", l.attempts, "outage_ms", time.Since(l.startedAt).Milliseconds())
	l.startedAt = time.Time{}
	l.attempts = 0
}

func retryableTunnelError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var engineErr *rstream.EngineError
	if errors.As(err, &engineErr) {
		if engineErr.Retryable() {
			return true
		}
		// Engines released before ERROR_CODE_RESOURCE_CONFLICT represented
		// ownership conflicts as INVALID_REQUEST. Keep watch-mode recovery
		// compatible while mixed-version deployments are upgraded.
		return engineErr.Code == rstream.EngineErrorCodeInvalidRequest && legacyResourceConflictMessage(engineErr.Message)
	}
	return true
}

func legacyResourceConflictMessage(message string) bool {
	switch strings.TrimSpace(message) {
	case "Hostname is already in use.",
		"TCP port is already in use.",
		"Registered WebTTY server is already online.":
		return true
	default:
		return false
	}
}

func (r *Runner) runOnce(ctx context.Context, desired runmodel.DesiredTunnel, logger *slog.Logger) error {
	return r.runOnceReady(ctx, desired, logger, nil)
}

func (r *Runner) runOnceReady(ctx context.Context, desired runmodel.DesiredTunnel, logger *slog.Logger, ready func()) error {
	opts := rstream.ClientOptions{
		Engine: desired.Context.Engine,
		Token:  desired.Context.Token,
	}
	if desired.Context.Transport != nil {
		opts.Transport = desired.Context.Transport
	}
	client, err := rstream.NewClient(opts)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	ctrl, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer ctrl.Close()
	logger.Info("Control channel connected")
	if desired.Props.Name == nil {
		name := desired.Name
		desired.Props.Name = &name
	}
	tunnel, err := ctrl.CreateTunnel(ctx, desired.Props)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer tunnel.Close()
	props, err := tunnel.Properties()
	if err != nil {
		return fmt.Errorf("failed to get tunnel properties: %w", err)
	}
	forwarding, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("failed to get forwarding address: %w", err)
	}
	logger.Info("Tunnel created", "tunnel_id", str(props.ID), "forwarding", forwarding)
	if ready != nil {
		ready()
	}
	if l, ok := tunnel.(net.Listener); ok {
		return r.serveWithCtx(ctx, l.Close, func() error { return r.serveTCP(ctx, l, desired.Forward, logger) })
	}
	if pl, ok := tunnel.(rstream.PacketListener); ok {
		return r.serveWithCtx(ctx, pl.Close, func() error { return r.serveUDP(ctx, pl, desired.Forward, logger) })
	}
	return fmt.Errorf("tunnel does not implement net.Listener or PacketListener")
}

func (r *Runner) serveWithCtx(ctx context.Context, closeFn func() error, fn func() error) error {
	errCh := make(chan error, 1)
	go func() { errCh <- fn() }()
	select {
	case <-ctx.Done():
		_ = closeFn()
		<-errCh
		return context.Canceled
	case err := <-errCh:
		return err
	}
}

func (r *Runner) serveTCP(ctx context.Context, l net.Listener, target runmodel.ForwardTarget, logger *slog.Logger) error {
	var acceptRetryDelay time.Duration
	for {
		inbound, err := l.Accept()
		if err != nil {
			delay, retry := netretry.NextAcceptDelay(err, acceptRetryDelay, acceptRetryMaxDelay)
			if retry && netretry.Wait(ctx, delay) {
				acceptRetryDelay = delay
				continue
			}
			return err
		}
		acceptRetryDelay = 0
		go r.proxyTCP(inbound, target, logger)
	}
}

func (r *Runner) proxyTCP(inbound net.Conn, target runmodel.ForwardTarget, logger *slog.Logger) {
	defer inbound.Close()
	outbound, err := net.Dial("tcp", target.String())
	if err != nil {
		logger.Debug("Dial error", "error", err)
		return
	}
	defer outbound.Close()
	streamrelay.Bidirectional(inbound, outbound)
}

func (r *Runner) serveUDP(ctx context.Context, l rstream.PacketListener, target runmodel.ForwardTarget, logger *slog.Logger) error {
	var acceptRetryDelay time.Duration
	for {
		inbound, raddr, err := l.Accept()
		if err != nil {
			delay, retry := netretry.NextAcceptDelay(err, acceptRetryDelay, acceptRetryMaxDelay)
			if retry && netretry.Wait(ctx, delay) {
				acceptRetryDelay = delay
				continue
			}
			return err
		}
		acceptRetryDelay = 0
		go r.proxyUDP(inbound, raddr, target, logger)
	}
}

func (r *Runner) proxyUDP(inbound net.PacketConn, remote net.Addr, target runmodel.ForwardTarget, logger *slog.Logger) {
	defer inbound.Close()
	udpRaddr, err := net.ResolveUDPAddr("udp", target.String())
	if err != nil {
		logger.Debug("ResolveUDPAddr error", "error", err)
		return
	}
	outbound, err := net.DialUDP("udp", nil, udpRaddr)
	if err != nil {
		logger.Debug("DialUDP error", "error", err)
		return
	}
	defer outbound.Close()
	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := inbound.ReadFrom(buf)
			if err != nil {
				break
			}
			if _, err := outbound.Write(buf[:n]); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := outbound.Read(buf)
			if err != nil {
				break
			}
			if _, err := inbound.WriteTo(buf[:n], remote); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
}

func str(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

type backoff struct {
	current time.Duration
	initial time.Duration
	max     time.Duration
}

func newBackoff(initial, max time.Duration) *backoff {
	if initial <= 0 {
		initial = 1 * time.Second
	}
	if max <= 0 {
		max = 30 * time.Second
	}
	if max < initial {
		max = initial
	}
	return &backoff{current: initial, initial: initial, max: max}
}

func jitterRetry(delay time.Duration) time.Duration {
	spread := delay / 5
	if spread <= 0 {
		return delay
	}
	return delay - spread + time.Duration(rand.Uint64N(uint64(spread)+1))
}

func (b *backoff) Next() time.Duration {
	if b.current < b.initial {
		b.current = b.initial
	}
	val := b.current
	if b.current >= b.max-b.current {
		b.current = b.max
	} else {
		b.current *= 2
	}
	return val
}

func (b *backoff) Reset() {
	b.current = b.initial
}
