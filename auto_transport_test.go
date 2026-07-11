// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseTunnelTransportMode(t *testing.T) {
	for _, value := range []string{"auto", " TLS ", "QuIc"} {
		if _, err := ParseTunnelTransportMode(value); err != nil {
			t.Fatalf("ParseTunnelTransportMode(%q) error = %v", value, err)
		}
	}
	if _, err := ParseTunnelTransportMode("udp"); err == nil {
		t.Fatal("expected invalid tunnel transport error")
	}
}

func TestAutoTransportPrefersQUICAndPinsSelection(t *testing.T) {
	quic := &autoTestDialer{}
	tlsDialer := &autoTestDialer{}
	delay := 50 * time.Millisecond
	transport := &AutoTransport{quicDialer: quic, tlsDialer: tlsDialer, FallbackDelay: &delay}

	conn, err := transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_ = conn.Close()
	conn, err = transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err != nil {
		t.Fatalf("second Dial() error = %v", err)
	}
	_ = conn.Close()
	if transport.SelectedMode() != TunnelTransportModeQUIC {
		t.Fatalf("SelectedMode() = %q, want quic", transport.SelectedMode())
	}
	if quic.callCount() != 2 || tlsDialer.callCount() != 0 {
		t.Fatalf("dial counts: quic=%d tls=%d", quic.callCount(), tlsDialer.callCount())
	}
}

func TestAutoTransportFallsBackToTLSAndPinsSelection(t *testing.T) {
	quic := &autoTestDialer{waitForCancellation: true}
	tlsDialer := &autoTestDialer{}
	delay := time.Millisecond
	transport := &AutoTransport{quicDialer: quic, tlsDialer: tlsDialer, FallbackDelay: &delay}

	conn, err := transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_ = conn.Close()
	conn, err = transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err != nil {
		t.Fatalf("second Dial() error = %v", err)
	}
	_ = conn.Close()
	if transport.SelectedMode() != TunnelTransportModeTLS {
		t.Fatalf("SelectedMode() = %q, want tls", transport.SelectedMode())
	}
	if quic.callCount() != 1 || tlsDialer.callCount() != 2 {
		t.Fatalf("dial counts: quic=%d tls=%d", quic.callCount(), tlsDialer.callCount())
	}
}

func TestAutoTransportStartsTLSImmediatelyAfterQUICFailure(t *testing.T) {
	quic := &autoTestDialer{err: errors.New("udp blocked")}
	tlsDialer := &autoTestDialer{}
	delay := time.Hour
	transport := &AutoTransport{quicDialer: quic, tlsDialer: tlsDialer, FallbackDelay: &delay}
	start := time.Now()
	conn, err := transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_ = conn.Close()
	if time.Since(start) > time.Second {
		t.Fatal("TLS fallback waited for the fallback delay after an immediate QUIC failure")
	}
}

func TestAutoTransportDefaultChildrenAreConcrete(t *testing.T) {
	transport := &AutoTransport{}
	if got := transport.tlsTransportOrDefault(); got == nil || isNilDialer(got) {
		t.Fatalf("tlsTransportOrDefault() = %#v, want concrete transport", got)
	}
	if got := transport.quicTransportOrDefault(); got == nil || isNilDialer(got) {
		t.Fatalf("quicTransportOrDefault() = %#v, want concrete transport", got)
	}
}

func TestAutoTransportReportsBothFailures(t *testing.T) {
	transport := &AutoTransport{
		quicDialer: &autoTestDialer{err: errors.New("udp blocked")},
		tlsDialer:  &autoTestDialer{err: errors.New("tcp blocked")},
	}
	_, err := transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
	if err == nil || !strings.Contains(err.Error(), "udp blocked") || !strings.Contains(err.Error(), "tcp blocked") {
		t.Fatalf("Dial() error = %v", err)
	}
}

func TestAutoTransportConcurrentDialsUseOneMode(t *testing.T) {
	quic := &autoTestDialer{delay: 10 * time.Millisecond}
	tlsDialer := &autoTestDialer{}
	delay := time.Second
	transport := &AutoTransport{quicDialer: quic, tlsDialer: tlsDialer, FallbackDelay: &delay}
	const count = 12
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := transport.Dial(t.Context(), "engine.example:443", &tls.Config{})
			if conn != nil {
				_ = conn.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Dial() error = %v", err)
		}
	}
	if transport.SelectedMode() != TunnelTransportModeQUIC || quic.callCount() != count || tlsDialer.callCount() != 0 {
		t.Fatalf("selection=%q dial counts quic=%d tls=%d", transport.SelectedMode(), quic.callCount(), tlsDialer.callCount())
	}
}

type autoTestDialer struct {
	mu                  sync.Mutex
	calls               int
	delay               time.Duration
	err                 error
	waitForCancellation bool
}

func (d *autoTestDialer) Dial(ctx context.Context, _ string, _ *tls.Config) (net.Conn, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	if d.waitForCancellation {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if d.delay > 0 {
		timer := time.NewTimer(d.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	client, server := net.Pipe()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	return client, nil
}

func (d *autoTestDialer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}
