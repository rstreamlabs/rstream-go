// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("not authenticated")

type Client struct {
	apiURL     string
	token      string
	httpClient *http.Client
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func NewClient(apiURL, token string, opts ...Option) *Client {
	client := &Client{
		apiURL:     strings.TrimRight(strings.TrimSpace(apiURL), "/"),
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client
}

func (c *Client) RequireToken() error {
	if strings.TrimSpace(c.token) == "" {
		return errors.New("not authenticated (run rstream login or set RSTREAM_AUTHENTICATION_TOKEN)")
	}
	return nil
}

func (c *Client) Whoami(ctx context.Context) (Whoami, error) {
	var out Whoami
	_, err := c.doJSON(ctx, http.MethodGet, "/api/whoami", nil, &out)
	return out, err
}

func (c *Client) ListProjects(ctx context.Context, params ListProjectsParams) (ListProjectsResponse, error) {
	var out ListProjectsResponse
	query := url.Values{}
	if params.Query != "" {
		query.Set("q", params.Query)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Sort != "" {
		query.Set("sort", params.Sort)
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	_, err := c.doJSON(ctx, http.MethodGet, "/api/projects", query, &out)
	return out, err
}

func (c *Client) ResolveProjectByEndpoint(ctx context.Context, endpoint string) (Project, error) {
	var out Project
	escaped := url.PathEscape(endpoint)
	_, err := c.doJSON(ctx, http.MethodGet, "/api/projects/resolve/"+escaped, nil, &out)
	return out, err
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, out any) (int, error) {
	if c.apiURL == "" {
		return 0, errors.New("apiUrl is required")
	}
	fullURL := c.apiURL
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	fullURL += path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}
	slog.Debug("control-plane request", "method", method, "url", fullURL)
	req, err := http.NewRequestWithContext(ctx, method, fullURL, nil)
	if err != nil {
		return 0, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return resp.StatusCode, fmt.Errorf("%w: %s", ErrUnauthorized, resp.Status)
		}
		slog.Debug("control-plane response", "status", resp.StatusCode, "statusText", resp.Status)
		return resp.StatusCode, fmt.Errorf("control-plane request failed: %s", resp.Status)
	}
	slog.Debug("control-plane response", "status", resp.StatusCode)
	if out == nil {
		return resp.StatusCode, nil
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}
