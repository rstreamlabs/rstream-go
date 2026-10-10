// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Event struct {
	ID          string          `json:"id,omitempty"`
	Type        string          `json:"type"`
	CreatedAt   string          `json:"created_at,omitempty"`
	UserID      string          `json:"user_id,omitempty"`
	WorkspaceID string          `json:"workspace_id,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	ClusterID   string          `json:"cluster_id,omitempty"`
	Object      json.RawMessage `json:"object"`
}

// APIError reports a completed Engine API response without treating HTTP policy
// errors as transport failures.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api %s %s: %s (%d)", e.Method, e.Path, e.Message, e.StatusCode)
}

type EngineHealth struct {
	Live  bool `json:"live"`
	Ready bool `json:"ready"`
}

const (
	apiRequestTimeout  = 5 * time.Second
	apiAttemptTimeout  = 2450 * time.Millisecond
	apiRequestAttempts = 2
	apiRetryDelayMin   = 20 * time.Millisecond
	apiRetryDelayRange = 30 * time.Millisecond
)

func cloneProxyHTTPHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}

func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return nil
	}
	return cfg.Clone()
}

func cloneTransport(transport *Transport) *Transport {
	if transport == nil {
		return &Transport{}
	}
	out := *transport
	out.LocalAddr = clonePtr(transport.LocalAddr)
	out.NetworkInterface = clonePtr(transport.NetworkInterface)
	out.ForceIPv4 = clonePtr(transport.ForceIPv4)
	out.ForceIPv6 = clonePtr(transport.ForceIPv6)
	out.DNSOverride = clonePtr(transport.DNSOverride)
	out.DNSOverTLS = clonePtr(transport.DNSOverTLS)
	out.DNSServerName = clonePtr(transport.DNSServerName)
	out.DNSSECEnabled = clonePtr(transport.DNSSECEnabled)
	out.ProxyHTTP = clonePtr(transport.ProxyHTTP)
	out.ProxySOCKS5 = clonePtr(transport.ProxySOCKS5)
	out.ProxyUsername = clonePtr(transport.ProxyUsername)
	out.ProxyPassword = clonePtr(transport.ProxyPassword)
	out.ProxyHTTPHeaders = cloneProxyHTTPHeaders(transport.ProxyHTTPHeaders)
	out.TLSProxyConfig = cloneTLSConfig(transport.TLSProxyConfig)
	out.ProxyFromEnvironment = clonePtr(transport.ProxyFromEnvironment)
	return &out
}

func (c *Client) apiDialer() Dialer {
	c.transportMu.Lock()
	transport := c.Transport
	c.transportMu.Unlock()
	switch transport := transport.(type) {
	case *Transport:
		return cloneTransport(transport)
	case *QUICTransport:
		if transport == nil {
			return &Transport{}
		}
		return &Transport{
			LocalAddr:            clonePtr(transport.LocalAddr),
			NetworkInterface:     clonePtr(transport.NetworkInterface),
			ForceIPv4:            clonePtr(transport.ForceIPv4),
			ForceIPv6:            clonePtr(transport.ForceIPv6),
			DNSOverride:          clonePtr(transport.DNSOverride),
			DNSOverTLS:           clonePtr(transport.DNSOverTLS),
			DNSServerName:        clonePtr(transport.DNSServerName),
			DNSSECEnabled:        clonePtr(transport.DNSSECEnabled),
			ProxyHTTP:            clonePtr(transport.ProxyHTTP),
			ProxySOCKS5:          clonePtr(transport.ProxySOCKS5),
			ProxyUsername:        clonePtr(transport.ProxyUsername),
			ProxyPassword:        clonePtr(transport.ProxyPassword),
			ProxyHTTPHeaders:     cloneProxyHTTPHeaders(transport.ProxyHTTPHeaders),
			TLSProxyConfig:       cloneTLSConfig(transport.TLSProxyConfig),
			ProxyFromEnvironment: clonePtr(transport.ProxyFromEnvironment),
		}
	case *AutoTransport:
		if transport == nil || transport.TLS == nil {
			return &Transport{}
		}
		return cloneTransport(transport.TLS)
	default:
		return &Transport{}
	}
}

// CloseIdleConnections closes pooled API connections owned by the client.
// It does not close active requests or the tunnel transport.
func (c *Client) CloseIdleConnections() {
	c.apiMu.Lock()
	transport := c.apiTransport
	discovery := c.discoveryTransport
	apiH3, discoveryH3 := c.apiHTTP3, c.discoveryHTTP3
	if discoveryH3 == nil {
		c.discoveryTransport = nil
	}
	if apiH3 == nil {
		c.apiTransport = nil
	}
	c.apiMu.Unlock()
	if transport != nil {
		transport.CloseIdleConnections()
	}
	if discovery != nil {
		discovery.CloseIdleConnections()
	}
	if apiH3 != nil {
		apiH3.CloseIdleConnections()
	}
	if discoveryH3 != nil {
		discoveryH3.CloseIdleConnections()
	}
}

func (c *Client) apiHttpClient() (*http.Client, error) {
	c.apiMu.Lock()
	if c.closed.Load() {
		c.apiMu.Unlock()
		return nil, net.ErrClosed
	}
	if c.apiTransport == nil {
		dialer := c.apiDialer()
		c.apiTransport = &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return c.dialEngineWithTransportConfig(ctx, &addr, &[]string{"h2", "http/1.1"}, dialer, c.apiTLSConfig(addr))
			},
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        4,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}
	}
	var transport http.RoundTripper = c.apiTransport
	if template, auto, delay := c.apiQUICOptions(); template != nil {
		if c.apiHTTP3 == nil {
			var fallback http.RoundTripper
			if auto {
				fallback = c.apiTransport
			}
			c.apiHTTP3 = newAPIHTTP3Transport(c, template, c.apiTLSConfig(""), fallback, delay)
		}
		transport = c.apiHTTP3
	}
	c.apiMu.Unlock()
	return &http.Client{
		Transport:     transport,
		Timeout:       apiRequestTimeout,
		CheckRedirect: rejectAPIRedirect,
	}, nil
}

func (c *Client) apiDo(ctx context.Context, method, path string, query url.Values, body io.Reader, engine, token *string) ([]byte, int, error) {
	if ctx == nil {
		return nil, 0, errors.New("API request context is required")
	}
	requestCtx, cancel := context.WithTimeout(ctx, apiRequestTimeout)
	defer cancel()
	if engine == nil {
		var err error
		engine, err = c.getEngine()
		if err != nil {
			return nil, 0, err
		}
	}
	clientDetails, err := c.getClientDetails(engine, token)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get client details: %w", err)
	}
	token = clientDetails.Token
	httpc, err := c.apiHttpClient()
	if err != nil {
		return nil, 0, err
	}
	base, err := c.resolveEngineAPIURL(requestCtx, engine)
	if err != nil {
		return nil, 0, err
	}
	url := base + path
	if len(query) > 0 {
		url += "?" + query.Encode()
	}
	retryable := body == nil && (method == http.MethodGet || method == http.MethodHead)
	for attempt := 1; attempt <= apiRequestAttempts; attempt++ {
		attemptCtx := requestCtx
		var attemptCancel context.CancelFunc
		if retryable && attempt < apiRequestAttempts {
			attemptCtx, attemptCancel = context.WithTimeout(requestCtx, apiAttemptTimeout)
		}
		responseBody, status, err := apiRequest(attemptCtx, httpc, method, url, path, body, token)
		if attemptCancel != nil {
			attemptCancel()
		}
		if err != nil {
			if cause := context.Cause(requestCtx); cause != nil {
				return nil, status, cause
			}
		}
		if err != nil && status == 0 && tlsConfigHasClientCertificate(c.TLSClientConfig) && retryableAPITransportError(requestCtx, status, err) {
			c.invalidateEngineDiscovery()
		}
		if err == nil || !retryable || attempt == apiRequestAttempts || !retryableAPITransportError(requestCtx, status, err) {
			return responseBody, status, err
		}
		if err := waitAPIRetry(requestCtx); err != nil {
			return nil, status, err
		}
		base, err = c.resolveEngineAPIURL(requestCtx, engine)
		if err != nil {
			return nil, 0, err
		}
		url = base + path
		if len(query) > 0 {
			url += "?" + query.Encode()
		}
	}
	return nil, 0, errors.New("API request retry invariant failed")
}

func apiRequest(ctx context.Context, client *http.Client, method, requestURL, path string, body io.Reader, token *string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, 0, err
	}
	if token != nil && *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var responseBody io.Reader = resp.Body
	if path == "/turn-server/credentials" {
		responseBody = io.LimitReader(responseBody, 16385)
	}
	b, rerr := io.ReadAll(responseBody)
	if rerr != nil {
		return nil, resp.StatusCode, rerr
	}
	if path == "/turn-server/credentials" && len(b) > 16384 {
		return nil, resp.StatusCode, errors.New("TURN response is too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, resp.StatusCode, &APIError{StatusCode: resp.StatusCode, Method: method, Path: path, Message: msg}
	}
	return b, resp.StatusCode, nil
}

func retryableAPITransportError(ctx context.Context, status int, err error) bool {
	if err == nil || context.Cause(ctx) != nil || errors.Is(err, context.Canceled) {
		return false
	}
	if status != 0 && (status < http.StatusOK || status >= http.StatusMultipleChoices) {
		return false
	}
	var certificateError *tls.CertificateVerificationError
	if errors.As(err, &certificateError) {
		return false
	}
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthority) {
		return false
	}
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return false
	}
	var dnsError *net.DNSError
	return !errors.As(err, &dnsError) || !dnsError.IsNotFound
}

func waitAPIRetry(ctx context.Context) error {
	delay := apiRetryDelayMin + time.Duration(rand.Int64N(int64(apiRetryDelayRange)+1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func setQueryJSON(q url.Values, key string, value any) error {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}
	q.Set(key, string(raw))
	return nil
}

func (c *Client) Login(ctx context.Context) (*string, error) {
	engine, err := c.getEngine()
	if err != nil {
		return nil, err
	}
	var token *string
	if c.Token != nil {
		token = c.Token
	}
	if token == nil {
		return nil, errors.New("no token provided")
	}
	if _, _, err := c.apiDo(ctx, http.MethodGet, "/auth", nil, nil, engine, token); err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}
	return engine, nil
}

func (c *Client) Logout(ctx context.Context) (*string, error) {
	engine, err := c.getEngine()
	if err != nil {
		return nil, err
	}
	return engine, nil
}

func (c *Client) CheckHealth(ctx context.Context) (*EngineHealth, error) {
	body, _, err := c.apiDo(ctx, http.MethodGet, "/health/live", nil, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("check engine liveness: %w", err)
	}
	if err := validateHealthResponse(body, "live"); err != nil {
		return nil, fmt.Errorf("check engine liveness: %w", err)
	}
	body, status, err := c.apiDo(ctx, http.MethodGet, "/health/ready", nil, nil, nil, nil)
	if err != nil {
		if status == http.StatusServiceUnavailable {
			return &EngineHealth{Live: true}, nil
		}
		return nil, fmt.Errorf("check engine readiness: %w", err)
	}
	if err := validateHealthResponse(body, "ready"); err != nil {
		return nil, fmt.Errorf("check engine readiness: %w", err)
	}
	return &EngineHealth{Live: true, Ready: true}, nil
}

func validateHealthResponse(body []byte, expected string) error {
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if response.Status != expected {
		return fmt.Errorf("unexpected status %q", response.Status)
	}
	return nil
}

func (c *Client) ListClients(ctx context.Context, params *ListClientsParams) (*ListClientsResponse, error) {
	q := url.Values{}
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	b, _, err := c.apiDo(ctx, http.MethodGet, "/clients", q, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	var out ListClientsResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

func (c *Client) ListTunnels(ctx context.Context, params *ListTunnelsParams) (*ListTunnelsResponse, error) {
	q := url.Values{}
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	b, _, err := c.apiDo(ctx, http.MethodGet, "/tunnels", q, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	var out ListTunnelsResponse
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

func (c *Client) GetTunnel(ctx context.Context, id string) (*TunnelProperties, error) {
	if id == "" {
		return nil, fmt.Errorf("ID is required")
	}
	b, _, err := c.apiDo(ctx, http.MethodGet, "/tunnels/"+url.PathEscape(id), nil, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	var t TunnelProperties
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &t, nil
}

type eventConn interface {
	Read(ctx context.Context) ([]byte, error)
	Close() error
}

func (c *Client) WatchSSE(ctx context.Context, params *WatchParams, handler func(Event) error) error {
	return c.Watch(ctx, "sse", params, handler)
}

func (c *Client) WatchWS(ctx context.Context, params *WatchParams, handler func(Event) error) error {
	return c.Watch(ctx, "websocket", params, handler)
}

func (c *Client) Watch(ctx context.Context, transport string, params *WatchParams, handler func(Event) error) error {
	ec, err := c.openEventConn(ctx, transport, params)
	if err != nil {
		return err
	}
	stopClose := context.AfterFunc(ctx, func() {
		_ = ec.Close()
	})
	defer func() {
		stopClose()
		_ = ec.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		default:
		}
		raw, err := ec.Read(ctx)
		if err != nil {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
		if len(raw) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return fmt.Errorf("invalid event json: %w", err)
		}
		if err := handler(ev); err != nil {
			return err
		}
	}
}

func (c *Client) openEventConn(ctx context.Context, transport string, params *WatchParams) (eventConn, error) {
	engine, err := c.getEngine()
	if err != nil {
		return nil, err
	}
	cd, err := c.getClientDetails(engine, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get client details: %w", err)
	}
	if (cd.Token == nil || *cd.Token == "") && !tlsConfigHasClientCertificate(c.TLSClientConfig) {
		return nil, fmt.Errorf("missing authentication token")
	}
	transport = strings.ToLower(strings.TrimSpace(transport))
	if transport != "sse" && transport != "websocket" && transport != "ws" {
		return nil, fmt.Errorf("invalid transport %q (valid: sse, websocket)", transport)
	}
	base, err := c.resolveEngineAPIURL(ctx, engine)
	if err != nil {
		return nil, err
	}
	token := ""
	if cd.Token != nil {
		token = *cd.Token
	}
	switch transport {
	case "sse":
		return c.openSSE(ctx, base, token, params)
	case "websocket", "ws":
		return c.openWS(ctx, base, token, params)
	default:
		return nil, fmt.Errorf("invalid transport %q (valid: sse, websocket)", transport)
	}
}

type sseConn struct {
	resp *http.Response
	rd   *bufio.Reader
	buf  strings.Builder
}

func (s *sseConn) Close() error { return s.resp.Body.Close() }

func (s *sseConn) Read(_ context.Context) ([]byte, error) {
	flush := func() ([]byte, error) {
		if s.buf.Len() == 0 {
			return nil, nil
		}
		out := strings.TrimSpace(s.buf.String())
		s.buf.Reset()
		return []byte(out), nil
	}
	for {
		line, err := s.rd.ReadString('\n')
		if err != nil && len(line) == 0 {
			if errors.Is(err, io.EOF) {
				if msg, _ := flush(); len(msg) > 0 {
					return msg, nil
				}
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return flush()
		}
		if strings.HasPrefix(line, "data:") {
			chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if s.buf.Len() > 0 {
				s.buf.WriteByte('\n')
			}
			s.buf.WriteString(chunk)
		}
		if errors.Is(err, io.EOF) {
			if msg, _ := flush(); len(msg) > 0 {
				return msg, nil
			}
			return nil, err
		}
		if err != nil {
			return nil, err
		}
	}
}

func (c *Client) openSSE(ctx context.Context, engine, token string, params *WatchParams) (eventConn, error) {
	httpc, err := c.apiHttpClient()
	if err != nil {
		return nil, err
	}
	httpc.Timeout = 0
	u := engine + "/sse"
	q := url.Values{}
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sse error (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return &sseConn{resp: resp, rd: bufio.NewReader(resp.Body)}, nil
}

type wsConn struct {
	c            *websocket.Conn
	done         chan struct{}
	pingDone     chan struct{}
	closeOnce    sync.Once
	closeErr     error
	pingInterval time.Duration
	readTimeout  time.Duration
}

const (
	watchWSPingInterval = 20 * time.Second
	watchWSReadTimeout  = 90 * time.Second
	watchWSWriteTimeout = 5 * time.Second
)

func newWSConn(conn *websocket.Conn, pingInterval, readTimeout time.Duration) *wsConn {
	w := &wsConn{
		c:            conn,
		done:         make(chan struct{}),
		pingDone:     make(chan struct{}),
		pingInterval: pingInterval,
		readTimeout:  readTimeout,
	}
	w.resetReadDeadline()
	w.c.SetPongHandler(func(string) error {
		w.resetReadDeadline()
		return nil
	})
	if pingInterval > 0 {
		go w.pingLoop()
	} else {
		close(w.pingDone)
	}
	return w
}

func (w *wsConn) Close() error {
	w.initiateClose()
	<-w.pingDone
	return w.closeErr
}

func (w *wsConn) initiateClose() {
	w.closeOnce.Do(func() {
		close(w.done)
		w.closeErr = w.c.Close()
	})
}

func (w *wsConn) Read(ctx context.Context) ([]byte, error) {
	for {
		mt, p, err := w.c.ReadMessage()
		if err != nil {
			return nil, err
		}
		if mt == websocket.TextMessage || mt == websocket.BinaryMessage {
			w.resetReadDeadline()
			return p, nil
		}
	}
}

func (w *wsConn) resetReadDeadline() {
	if w.readTimeout <= 0 {
		return
	}
	_ = w.c.SetReadDeadline(time.Now().Add(w.readTimeout))
}

func (w *wsConn) pingLoop() {
	ticker := time.NewTicker(w.pingInterval)
	defer ticker.Stop()
	defer close(w.pingDone)
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			deadline := time.Now().Add(watchWSWriteTimeout)
			if err := w.c.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				w.initiateClose()
				return
			}
		}
	}
}

func (c *Client) openWS(ctx context.Context, engine, token string, params *WatchParams) (eventConn, error) {
	u, err := url.Parse(engine + "/websocket")
	if err != nil {
		return nil, err
	}
	u.Scheme = "wss"
	q := u.Query()
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	u.RawQuery = q.Encode()
	dialerTransport := c.apiDialer()
	dialer := &websocket.Dialer{
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			np := []string{"http/1.1"}
			return c.dialEngineWithTransportConfig(ctx, &addr, &np, dialerTransport, c.apiTLSConfig(addr))
		},
		EnableCompression: false,
		Proxy:             http.ProxyFromEnvironment,
	}
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	conn, resp, err := dialer.DialContext(ctx, u.String(), header)
	if err != nil {
		if resp != nil {
			statusCode := resp.StatusCode
			_ = resp.Body.Close()
			return nil, fmt.Errorf("websocket dial failed: %s (%d)", http.StatusText(statusCode), statusCode)
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "wss") {
		_ = conn.Close()
		return nil, fmt.Errorf("insecure websocket scheme %q is not supported", u.Scheme)
	}
	return newWSConn(conn, watchWSPingInterval, watchWSReadTimeout), nil
}
