// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

type TunnelTransportMode string

const (
	TunnelTransportModeAuto TunnelTransportMode = "auto"
	TunnelTransportModeTLS  TunnelTransportMode = "tls"
	TunnelTransportModeQUIC TunnelTransportMode = "quic"
)

const defaultAutoTransportFallbackDelay = 300 * time.Millisecond

func ParseTunnelTransportMode(value string) (TunnelTransportMode, error) {
	mode := TunnelTransportMode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case TunnelTransportModeAuto, TunnelTransportModeTLS, TunnelTransportModeQUIC:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid tunnel transport %q (valid: auto, tls, quic)", value)
	}
}

// AutoTransport gives QUIC a head start, falls back to TLS when necessary, and
// pins the first successful transport for the lifetime of the instance.
type AutoTransport struct {
	TLS           *Transport
	QUIC          *QUICTransport
	FallbackDelay *time.Duration
	tlsDialer     Dialer
	quicDialer    Dialer

	mu              sync.Mutex
	selected        Dialer
	selectedMode    TunnelTransportMode
	selecting       bool
	selectionDone   chan struct{}
	selectionCancel context.CancelFunc
	generation      uint64
}

func (t *AutoTransport) Dial(ctx context.Context, addr string, tlsCfg *tls.Config) (net.Conn, error) {
	for {
		t.mu.Lock()
		if t.selected != nil {
			selected := t.selected
			t.mu.Unlock()
			return selected.Dial(ctx, addr, tlsCfg)
		}
		if t.selecting {
			done := t.selectionDone
			t.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}

		selectionCtx, cancel := context.WithCancel(ctx)
		t.selecting = true
		t.selectionDone = make(chan struct{})
		t.selectionCancel = cancel
		done := t.selectionDone
		generation := t.generation
		t.mu.Unlock()

		conn, selected, mode, err := t.selectTransport(selectionCtx, addr, tlsCfg)
		cancel()

		t.mu.Lock()
		if generation != t.generation {
			if conn != nil {
				_ = conn.Close()
			}
			closeAutoTransport(selected)
			conn = nil
			err = net.ErrClosed
		} else if err == nil {
			t.selected = selected
			t.selectedMode = mode
		}
		t.selecting = false
		t.selectionCancel = nil
		close(done)
		t.mu.Unlock()
		return conn, err
	}
}

func (t *AutoTransport) SelectedTransport() Dialer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.selected
}

func (t *AutoTransport) SelectedMode() TunnelTransportMode {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.selectedMode == "" {
		return TunnelTransportModeAuto
	}
	return t.selectedMode
}

func (t *AutoTransport) Close() error {
	t.mu.Lock()
	t.generation++
	if t.selectionCancel != nil {
		t.selectionCancel()
	}
	selected := t.selected
	t.selected = nil
	t.selectedMode = ""
	t.mu.Unlock()
	return closeAutoTransport(selected)
}

type autoTransportResult struct {
	mode      TunnelTransportMode
	transport Dialer
	conn      net.Conn
	err       error
}

func (t *AutoTransport) selectTransport(ctx context.Context, addr string, tlsCfg *tls.Config) (net.Conn, Dialer, TunnelTransportMode, error) {
	tlsTransport := t.tlsTransportOrDefault()
	quicTransport := t.quicTransportOrDefault()
	delay := defaultAutoTransportFallbackDelay
	if t.FallbackDelay != nil {
		delay = *t.FallbackDelay
		if delay < 0 {
			delay = 0
		}
	}

	results := make(chan autoTransportResult, 2)
	selectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	start := func(mode TunnelTransportMode, transport Dialer) {
		go func() {
			conn, err := transport.Dial(selectionCtx, addr, tlsCfg)
			results <- autoTransportResult{mode: mode, transport: transport, conn: conn, err: err}
		}()
	}
	start(TunnelTransportModeQUIC, quicTransport)
	started := 1
	completed := 0
	tlsStarted := false
	timer := time.NewTimer(delay)
	defer timer.Stop()
	var quicErr error
	var tlsErr error

	startTLS := func() {
		if tlsStarted {
			return
		}
		tlsStarted = true
		started++
		start(TunnelTransportModeTLS, tlsTransport)
	}

	for completed < started || !tlsStarted {
		select {
		case <-ctx.Done():
			cancel()
			go cleanupAutoTransportResults(results, started-completed, "")
			return nil, nil, "", ctx.Err()
		case <-timer.C:
			startTLS()
		case result := <-results:
			completed++
			if result.err == nil {
				cancel()
				go cleanupAutoTransportResults(results, started-completed, result.mode)
				return result.conn, result.transport, result.mode, nil
			}
			switch result.mode {
			case TunnelTransportModeQUIC:
				quicErr = result.err
				startTLS()
			case TunnelTransportModeTLS:
				tlsErr = result.err
			}
		}
	}
	return nil, nil, "", errors.Join(
		fmt.Errorf("QUIC tunnel transport failed: %w", quicErr),
		fmt.Errorf("TLS tunnel transport failed: %w", tlsErr),
	)
}

func (t *AutoTransport) tlsTransportOrDefault() Dialer {
	if t.tlsDialer != nil {
		return t.tlsDialer
	}
	if t.TLS != nil {
		return t.TLS
	}
	return &Transport{}
}

func (t *AutoTransport) quicTransportOrDefault() Dialer {
	if t.quicDialer != nil {
		return t.quicDialer
	}
	if t.QUIC != nil {
		return t.QUIC
	}
	return &QUICTransport{}
}

func cleanupAutoTransportResults(results <-chan autoTransportResult, count int, winner TunnelTransportMode) {
	for i := 0; i < count; i++ {
		result := <-results
		if result.conn != nil {
			_ = result.conn.Close()
		}
		if result.mode != winner && result.mode == TunnelTransportModeQUIC {
			_ = closeAutoTransport(result.transport)
		}
	}
}

func closeAutoTransport(transport Dialer) error {
	if transport == nil {
		return nil
	}
	closer, ok := transport.(interface{ Close() error })
	if !ok {
		return nil
	}
	return closer.Close()
}
