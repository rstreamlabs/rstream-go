// See LICENSE file in the project root for license information.

package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("not authenticated")

type apiErrorResponse struct {
	Error string `json:"error"`
}

type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type Client struct {
	apiURL     string
	token      string
	httpClient *http.Client
	logger     *slog.Logger
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

func NewClient(apiURL, token string, opts ...Option) *Client {
	client := &Client{
		apiURL:     strings.TrimRight(strings.TrimSpace(apiURL), "/"),
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     slog.With("component", "control-plane.client"),
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
	_, err := c.doJSON(ctx, http.MethodGet, "/api/projects/tunnels", query, &out)
	return out, err
}

func (c *Client) ResolveProjectByEndpoint(ctx context.Context, endpoint string) (Project, error) {
	var out Project
	escaped := url.PathEscape(endpoint)
	_, err := c.doJSON(ctx, http.MethodGet, "/api/projects/tunnels/resolve/"+escaped, nil, &out)
	return out, err
}

func (c *Client) CreateProjectTURNCredentials(ctx context.Context, projectID string) (TURNCredentials, error) {
	var out TURNCredentials
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/turn-server/credentials"
	_, err := c.doJSON(ctx, http.MethodPost, path, nil, &out)
	return out, err
}

func (c *Client) CreateProjectTURNCredentialsByEndpoint(ctx context.Context, endpoint string) (TURNCredentials, error) {
	var out TURNCredentials
	path := "/api/projects/tunnels/resolve/" + url.PathEscape(endpoint) + "/turn-server/credentials"
	_, err := c.doJSON(ctx, http.MethodPost, path, nil, &out)
	return out, err
}

func (c *Client) requestURL(path string, query url.Values) (string, error) {
	if c.apiURL == "" {
		return "", errors.New("API URL is required")
	}
	fullURL := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		fullURL = c.apiURL + path
	}
	parsed, err := url.Parse(fullURL)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	for key, entries := range query {
		for _, entry := range entries {
			values.Add(key, entry)
		}
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, out any) (int, error) {
	return c.doJSONBody(ctx, method, path, query, nil, out)
}

func (c *Client) doJSONBody(ctx context.Context, method, path string, query url.Values, body any, out any) (int, error) {
	fullURL, err := c.requestURL(path, query)
	if err != nil {
		return 0, err
	}
	c.logger.Debug("control-plane request", "method", method, "url", fullURL)
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
		responseBody, _ := io.ReadAll(resp.Body)
		message := controlPlaneErrorMessage(resp.Status, responseBody)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return resp.StatusCode, fmt.Errorf("%w: %s", ErrUnauthorized, message)
		}
		c.logger.Debug("control-plane response", "status", resp.StatusCode, "statusText", resp.Status)
		return resp.StatusCode, errors.New(message)
	}
	c.logger.Debug("control-plane response", "status", resp.StatusCode)
	if out == nil {
		return resp.StatusCode, nil
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func (c *Client) doForm(ctx context.Context, method, path string, form url.Values, out any) (int, error) {
	fullURL, err := c.requestURL(path, nil)
	if err != nil {
		return 0, err
	}
	c.logger.Debug("control-plane request", "method", method, "url", fullURL)
	req, err := http.NewRequestWithContext(ctx, method, fullURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(resp.Body)
		message := oauthControlPlaneErrorMessage(resp.Status, responseBody)
		c.logger.Debug("control-plane response", "status", resp.StatusCode, "statusText", resp.Status)
		return resp.StatusCode, message
	}
	c.logger.Debug("control-plane response", "status", resp.StatusCode)
	if out == nil {
		return resp.StatusCode, nil
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func controlPlaneErrorMessage(status string, body []byte) string {
	var payload apiErrorResponse
	if err := json.Unmarshal(body, &payload); err == nil {
		if message := strings.TrimSpace(payload.Error); message != "" {
			return message
		}
	}
	return fmt.Sprintf("control-plane request failed: %s", status)
}

func oauthControlPlaneErrorMessage(status string, body []byte) error {
	var payload oauthErrorResponse
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return &OAuthError{Code: payload.Error, Description: payload.ErrorDescription}
	}
	return fmt.Errorf("control-plane request failed: %s", status)
}
