// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Only a failed handshake can select TCP: an HTTP response failure may follow
// an executed mutation and must never cause transport fallback.
type apiQUICDialError struct{ err error }

func (e *apiQUICDialError) Error() string { return e.err.Error() }
func (e *apiQUICDialError) Unwrap() error { return e.err }

type apiHTTP3Transport struct {
	transport     *http3.Transport
	client        *Client
	template      *QUICTransport
	fallback      http.RoundTripper
	fallbackDelay time.Duration
	useTCP        atomic.Bool
	mu            sync.Mutex
	closed        bool
	dialers       map[*QUICTransport]struct{}
	workers       sync.WaitGroup
}

func newAPIHTTP3Transport(client *Client, template *QUICTransport, cfg *tls.Config, fallback http.RoundTripper, delay time.Duration) *apiHTTP3Transport {
	t := &apiHTTP3Transport{client: client, template: template, fallback: fallback, fallbackDelay: delay, dialers: make(map[*QUICTransport]struct{})}
	t.transport = &http3.Transport{TLSClientConfig: cfg, MaxResponseHeaderBytes: 64 * 1024, Dial: t.dial}
	return t
}

func (t *apiHTTP3Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.useTCP.Load() {
		return t.fallback.RoundTrip(req)
	}
	response, err := t.transport.RoundTrip(req)
	var dialError *apiQUICDialError
	if t.fallback == nil || !errors.As(err, &dialError) || !retryableAPITransportError(req.Context(), 0, dialError.err) {
		return response, err
	}
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return response, err
		}
		body, bodyErr := req.GetBody()
		if bodyErr != nil {
			return nil, bodyErr
		}
		req = req.Clone(req.Context())
		req.Body = body
	}
	t.useTCP.Store(true)
	return t.fallback.RoundTrip(req)
}

func (t *apiHTTP3Transport) dial(ctx context.Context, addr string, cfg *tls.Config, _ *quic.Config) (*quic.Conn, error) {
	ctx, finish, err := t.client.beginDial(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if t.fallback != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.fallbackDelay)
		defer cancel()
	}
	owned := cloneQUICTransport(t.template)
	if err := validateFIPSClient(owned, cfg); err != nil {
		return nil, err
	}
	if err := validateFIPSQUICConfig(cfg); err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.closed || len(t.dialers) >= 4 {
		t.mu.Unlock()
		return nil, net.ErrClosed
	}
	t.dialers[owned] = struct{}{}
	t.workers.Add(1)
	t.mu.Unlock()
	conn, err := dialWithECHResult(ctx, addr, cfg, echResolverOptions(owned), func(ctx context.Context, addr string, cfg *tls.Config) (*quic.Conn, error) {
		origin, err := quicTransportOrigin(addr, cfg)
		if err != nil {
			return nil, err
		}
		return owned.connection(ctx, addr, cfg, origin)
	})
	if err != nil {
		t.release(owned)
		return nil, &apiQUICDialError{err: err}
	}
	go func() { <-conn.Context().Done(); t.release(owned) }()
	return conn, nil
}

func (t *apiHTTP3Transport) release(owned *QUICTransport) {
	_ = owned.Close()
	t.mu.Lock()
	delete(t.dialers, owned)
	t.mu.Unlock()
	t.workers.Done()
}

func (t *apiHTTP3Transport) CloseIdleConnections() {
	t.transport.CloseIdleConnections()
}

func (t *apiHTTP3Transport) Close() error {
	t.mu.Lock()
	t.closed = true
	dialers := make([]*QUICTransport, 0, len(t.dialers))
	for dialer := range t.dialers {
		dialers = append(dialers, dialer)
	}
	t.mu.Unlock()
	for _, dialer := range dialers {
		_ = dialer.Close()
	}
	err := t.transport.Close()
	t.workers.Wait()
	return err
}

func (c *Client) apiQUICOptions() (*QUICTransport, bool, time.Duration) {
	c.transportMu.Lock()
	transport := c.Transport
	c.transportMu.Unlock()
	switch t := transport.(type) {
	case *QUICTransport:
		return cloneQUICTransport(t), false, 0
	case *AutoTransport:
		if t == nil || (c.TLSClientConfig != nil && c.TLSClientConfig.MaxVersion != 0 && c.TLSClientConfig.MaxVersion < tls.VersionTLS13) {
			return nil, false, 0
		}
		delay := defaultAutoTransportFallbackDelay
		if t.FallbackDelay != nil && *t.FallbackDelay > 0 {
			delay = *t.FallbackDelay
		}
		return cloneQUICTransport(t.QUIC), true, delay
	default:
		return nil, false, 0
	}
}

func (c *Client) closeAPIHTTP3Transports() error {
	c.apiMu.Lock()
	api, discovery := c.apiHTTP3, c.discoveryHTTP3
	c.apiHTTP3 = nil
	c.discoveryHTTP3 = nil
	c.apiMu.Unlock()
	var err error
	if api != nil {
		err = api.Close()
	}
	if discovery != nil {
		err = errors.Join(err, discovery.Close())
	}
	return err
}
