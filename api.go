// See LICENSE file in the project root for license information.

package rstream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Event struct {
	Type   string          `json:"type"`
	Object json.RawMessage `json:"object"`
}

func (c *Client) apiHttpClient() (*http.Client, error) {
	return &http.Client{
		Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return c.dialEngine(ctx, &addr, &[]string{"h2", "http/1.1"})
			},
			ForceAttemptHTTP2: true,
		},
		Timeout: 5 * time.Second,
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
		clientDetails, err := c.getClientDetails(engine, nil)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get client details: %w", err)
		}
		token = clientDetails.Token
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

func (c *Client) ListTunnels(ctx context.Context, params *ListTunnelsParams) (*ListTunnelsResponse, error) {
	q := url.Values{}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("encode params: %w", err)
		}
		q.Set("params", string(raw))
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

func (c *Client) WatchSSE(ctx context.Context, handler func(Event) error) error {
	return c.Watch(ctx, "sse", handler)
}

func (c *Client) WatchWS(ctx context.Context, handler func(Event) error) error {
	return c.Watch(ctx, "websocket", handler)
}

type eventConn interface {
	Read(ctx context.Context) ([]byte, error)
	Close() error
}

func (c *Client) Watch(ctx context.Context, transport string, handler func(Event) error) error {
	ec, err := c.openEventConn(ctx, transport)
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = ec.Close()
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		_ = ec.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		raw, err := ec.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return context.Canceled
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

func (c *Client) openEventConn(ctx context.Context, transport string) (eventConn, error) {
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
		return c.openSSE(ctx, *engine, *cd.Token)
	case "websocket", "ws":
		return c.openWS(ctx, *engine, *cd.Token)
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
		if err != nil {
			if err == io.EOF {
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
	}
}

func (c *Client) openSSE(ctx context.Context, engine, token string) (eventConn, error) {
	httpc, err := c.apiHttpClient()
	if err != nil {
		return nil, err
	}
	httpc.Timeout = 0
	base := "https://" + engine
	u := base + "/api/sse"
	q := url.Values{}
	q.Set("rstream.token", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
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
	c *websocket.Conn
}

func (w *wsConn) Close() error { return w.c.Close() }

func (w *wsConn) Read(ctx context.Context) ([]byte, error) {
	for {
		mt, p, err := w.c.ReadMessage()
		if err != nil {
			return nil, err
		}
		if mt == websocket.TextMessage || mt == websocket.BinaryMessage {
			return p, nil
		}
	}
}

func (c *Client) openWS(ctx context.Context, engine, token string) (eventConn, error) {
	u := url.URL{
		Scheme: "wss",
		Host:   engine,
		Path:   "/api/websocket",
	}
	q := u.Query()
	q.Set("rstream.token", token)
	u.RawQuery = q.Encode()
	dialer := &websocket.Dialer{
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			np := []string{"http/1.1"}
			return c.dialEngine(ctx, &addr, &np)
		},
		EnableCompression: false,
		Proxy:             http.ProxyFromEnvironment,
	}
	conn, resp, err := dialer.DialContext(ctx, u.String(), http.Header{})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket dial failed: %s (%d)", http.StatusText(resp.StatusCode), resp.StatusCode)
		}
		return nil, fmt.Errorf("websocket dial failed: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "wss") {
		_ = conn.Close()
		return nil, fmt.Errorf("insecure websocket scheme %q is not supported", u.Scheme)
	}
	return &wsConn{c: conn}, nil
}
