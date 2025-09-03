// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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

func (c *Client) apiDo(ctx context.Context, method, path string, query url.Values, body io.Reader) ([]byte, int, error) {
	engine, err := c.getEngine()
	if err != nil {
		return nil, 0, err
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
	clientDetails, err := c.getClientDetails(engine, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get client details: %w", err)
	}
	if clientDetails.Token != nil && *clientDetails.Token != "" {
		req.Header.Set("Authorization", "Bearer "+*clientDetails.Token)
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

func (c *Client) ListTunnels(ctx context.Context, params *ListTunnelsParams) (*ListTunnelsResponse, error) {
	q := url.Values{}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("encode params: %w", err)
		}
		q.Set("params", string(raw))
	}
	b, _, err := c.apiDo(ctx, http.MethodGet, "/tunnels", q, nil)
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
	b, _, err := c.apiDo(ctx, http.MethodGet, "/tunnels/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return nil, err
	}
	var t TunnelProperties
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &t, nil
}
