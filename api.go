// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func (c *Client) apiDialer() Dialer {
	switch transport := c.Transport.(type) {
	case *Transport:
		if transport == nil {
			return &Transport{}
		}
		out := *transport
		out.TLSProxyConfig = cloneTLSConfig(transport.TLSProxyConfig)
		out.ProxyHTTPHeaders = cloneProxyHTTPHeaders(transport.ProxyHTTPHeaders)
		return &out
	case *QUICTransport:
		if transport == nil {
			return &Transport{}
		}
		return &Transport{
			LocalAddr:            transport.LocalAddr,
			NetworkInterface:     transport.NetworkInterface,
			ForceIPv4:            transport.ForceIPv4,
			ForceIPv6:            transport.ForceIPv6,
			DNSOverride:          transport.DNSOverride,
			DNSOverTLS:           transport.DNSOverTLS,
			DNSServerName:        transport.DNSServerName,
			DNSSECEnabled:        transport.DNSSECEnabled,
			ProxyHTTP:            transport.ProxyHTTP,
			ProxySOCKS5:          transport.ProxySOCKS5,
			ProxyUsername:        transport.ProxyUsername,
			ProxyPassword:        transport.ProxyPassword,
			ProxyHTTPHeaders:     cloneProxyHTTPHeaders(transport.ProxyHTTPHeaders),
			TLSProxyConfig:       cloneTLSConfig(transport.TLSProxyConfig),
			ProxyFromEnvironment: transport.ProxyFromEnvironment,
		}
	case *AutoTransport:
		if transport == nil || transport.TLS == nil {
			return &Transport{}
		}
		out := *transport.TLS
		out.TLSProxyConfig = cloneTLSConfig(transport.TLS.TLSProxyConfig)
		out.ProxyHTTPHeaders = cloneProxyHTTPHeaders(transport.TLS.ProxyHTTPHeaders)
		return &out
	default:
		return &Transport{}
	}
}

func (c *Client) apiHttpClient() (*http.Client, error) {
	c.apiMu.Lock()
	if c.apiTransport == nil {
		dialer := c.apiDialer()
		c.apiTransport = &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// API calls always use TCP TLS regardless of the configured
				// transport, because API endpoints require HTTP/1.1 or H2 (not H3).
				return c.dialEngineWithTransport(ctx, &addr, &[]string{"h2", "http/1.1"}, dialer)
			},
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        4,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}
	}
	transport := c.apiTransport
	c.apiMu.Unlock()
	return &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}, nil
}

func (c *Client) apiDo(ctx context.Context, method, path string, query url.Values, body io.Reader, engine, token *string) ([]byte, int, error) {
	if engine == nil {
		var err error
		engine, err = c.getEngine()
		if err != nil {
			return nil, 0, err
		}
	}
	if token == nil {
		ClientDetails, err := c.getClientDetails(engine, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get client details: %w", err)
		}
		token = ClientDetails.Token
	}
	httpc, err := c.apiHttpClient()
	if err != nil {
		return nil, 0, err
	}
	base := "https://" + *engine
	url := base + "/api" + path
	if len(query) > 0 {
		url += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, 0, err
	}
	if token != nil && *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		return nil, resp.StatusCode, rerr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, resp.StatusCode, fmt.Errorf("api %s %s: %s (%d)", method, path, msg, resp.StatusCode)
	}
	return b, resp.StatusCode, nil
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
	if cd.Token == nil || *cd.Token == "" {
		return nil, fmt.Errorf("missing authentication token")
	}
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "sse":
		return c.openSSE(ctx, *engine, *cd.Token, params)
	case "websocket", "ws":
		return c.openWS(ctx, *engine, *cd.Token, params)
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
	base := "https://" + engine
	u := base + "/api/sse"
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
	req.Header.Set("Authorization", "Bearer "+token)
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
	}
	return w
}

func (w *wsConn) Close() error {
	w.closeOnce.Do(func() {
		close(w.done)
		w.closeErr = w.c.Close()
	})
	return w.closeErr
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
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			deadline := time.Now().Add(watchWSWriteTimeout)
			if err := w.c.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				_ = w.Close()
				return
			}
		}
	}
}

func (c *Client) openWS(ctx context.Context, engine, token string, params *WatchParams) (eventConn, error) {
	u := url.URL{
		Scheme: "wss",
		Host:   engine,
		Path:   "/api/websocket",
	}
	q := u.Query()
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	u.RawQuery = q.Encode()
	dialerTransport := c.apiDialer()
	dialer := &websocket.Dialer{
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			np := []string{"http/1.1"}
			return c.dialEngineWithTransport(ctx, &addr, &np, dialerTransport)
		},
		EnableCompression: false,
		Proxy:             http.ProxyFromEnvironment,
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
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
