// See LICENSE file in the project root for license information.

package rstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const engineDiscoveryPath = "/.well-known/rstream/engine"

type engineAPIDiscovery struct {
	mu      sync.Mutex
	source  string
	apiURL  string
	expires time.Time
	flight  *engineAPIDiscoveryFlight
}

type engineAPIDiscoveryFlight struct {
	source string
	done   chan struct{}
	apiURL string
	err    error
}

// EngineAPIURL returns the HTTP API base URL for this client's authentication.
// Certificate clients discover their dedicated authority once per cache lifetime.
func (c *Client) EngineAPIURL(ctx context.Context) (string, error) {
	return c.resolveEngineAPIURL(ctx, nil)
}

func (c *Client) resolveEngineAPIURL(ctx context.Context, engine *string) (string, error) {
	if ctx == nil {
		return "", errors.New("API request context is required")
	}
	if c == nil || c.closed.Load() {
		return "", net.ErrClosed
	}
	if engine == nil {
		var err error
		engine, err = c.getEngine()
		if err != nil {
			return "", err
		}
	}
	engineURL := strings.TrimSuffix(strings.TrimSpace(*engine), "/")
	if !strings.Contains(engineURL, "://") {
		engineURL = "https://" + engineURL
	}
	if !strings.HasSuffix(engineURL, "/api") {
		engineURL += "/api"
	}
	source, err := parseEngineAPIURL(engineURL)
	if err != nil {
		return "", err
	}
	if !tlsConfigHasClientCertificate(c.TLSClientConfig) {
		return source.String(), nil
	}
	if c.Token != nil && *c.Token != "" {
		return "", errors.New("token and mTLS authentication cannot be used together")
	}
	if c.MTLSAPIURL != "" {
		target, err := validateMTLSAPIURL(c.MTLSAPIURL, source)
		if err != nil {
			return "", err
		}
		return target.String(), nil
	}
	ctx, cancel := context.WithTimeout(ctx, apiRequestTimeout)
	defer cancel()
	for {
		if err := context.Cause(ctx); err != nil {
			return "", err
		}
		c.apiDiscovery.mu.Lock()
		if c.apiDiscovery.source == source.String() && time.Now().Before(c.apiDiscovery.expires) {
			value := c.apiDiscovery.apiURL
			c.apiDiscovery.mu.Unlock()
			return value, nil
		}
		if flight := c.apiDiscovery.flight; flight != nil {
			c.apiDiscovery.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", context.Cause(ctx)
			case <-flight.done:
				if flight.source == source.String() {
					return flight.apiURL, flight.err
				}
				continue
			}
		}
		flight := &engineAPIDiscoveryFlight{source: source.String(), done: make(chan struct{})}
		c.apiDiscovery.flight = flight
		c.apiDiscovery.mu.Unlock()
		value, lifetime, err := c.discoverEngineAPI(ctx, source)
		c.apiDiscovery.mu.Lock()
		if err == nil && !c.closed.Load() {
			c.apiDiscovery.source = source.String()
			c.apiDiscovery.apiURL = value
			c.apiDiscovery.expires = time.Now().Add(lifetime)
		}
		flight.apiURL, flight.err = value, err
		c.apiDiscovery.flight = nil
		close(flight.done)
		c.apiDiscovery.mu.Unlock()
		return value, err
	}
}

func parseEngineAPIURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "/api" && u.Path != "/api/") || strings.ContainsAny(u.Hostname(), "\\%") {
		return nil, errors.New("engine API URL must be an absolute HTTPS URL ending in /api")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid Engine API port")
		}
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = "/api"
	return u, nil
}

func validateMTLSAPIURL(value string, source *url.URL) (*url.URL, error) {
	target, err := parseEngineAPIURL(value)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.Split(target.Hostname(), ".")[0], strings.Split(source.Hostname(), ".")[0]) {
		return nil, errors.New("mTLS API URL does not match the project endpoint")
	}
	return target, nil
}

func (c *Client) discoverEngineAPI(ctx context.Context, source *url.URL) (string, time.Duration, error) {
	ctx, finish, err := c.beginDial(ctx)
	if err != nil {
		return "", 0, err
	}
	defer finish()
	client, err := c.discoveryHTTPClient()
	if err != nil {
		return "", 0, err
	}
	u := *source
	u.Path = engineDiscoveryPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("discover mTLS Engine API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("mTLS Engine API discovery unavailable (%d)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(body) > 16384 {
		return "", 0, errors.New("invalid Engine API discovery response")
	}
	var document struct {
		Version         int      `json:"version"`
		ProjectEndpoint string   `json:"projectEndpoint"`
		MTLSAPIURL      string   `json:"mtlsApiUrl"`
		Capabilities    []string `json:"capabilities"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&document); err != nil || decoder.Decode(new(any)) != io.EOF || document.Version != 1 || document.ProjectEndpoint != strings.Split(source.Hostname(), ".")[0] || !slices.Contains(document.Capabilities, "engine-api-mtls") {
		return "", 0, errors.New("invalid Engine API discovery document")
	}
	target, err := validateMTLSAPIURL(document.MTLSAPIURL, source)
	if err != nil {
		return "", 0, err
	}
	return target.String(), discoveryCacheLifetime(resp.Header.Get("Cache-Control")), nil
}

func discoveryCacheLifetime(value string) time.Duration {
	lifetime := time.Hour
	for _, directive := range strings.Split(strings.ToLower(value), ",") {
		directive = strings.TrimSpace(directive)
		if directive == "no-store" || directive == "no-cache" {
			return 0
		}
		if raw, ok := strings.CutPrefix(directive, "max-age="); ok {
			seconds, err := strconv.ParseInt(strings.Trim(raw, "\""), 10, 64)
			if err != nil || seconds < 0 {
				return 0
			}
			lifetime = time.Duration(min(seconds, 86400)) * time.Second
		}
	}
	return lifetime
}

func (c *Client) discoveryHTTPClient() (*http.Client, error) {
	c.apiMu.Lock()
	defer c.apiMu.Unlock()
	if c.closed.Load() {
		return nil, net.ErrClosed
	}
	if c.discoveryTransport == nil {
		dialer := c.apiDialer()
		cfg := c.apiTLSConfig("")
		cfg.Certificates = nil
		cfg.GetClientCertificate = nil
		cfg.ClientSessionCache = nil
		cfg.InsecureSkipVerify = false
		c.discoveryTransport = &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return c.dialEngineWithTransportConfig(ctx, &addr, &[]string{"h2", "http/1.1"}, dialer, cfg)
			},
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        2,
			MaxIdleConnsPerHost: 2,
			MaxConnsPerHost:     2,
			IdleConnTimeout:     90 * time.Second,
		}
	}
	var transport http.RoundTripper = c.discoveryTransport
	if template, auto, delay := c.apiQUICOptions(); template != nil {
		if c.discoveryHTTP3 == nil {
			cfg := c.apiTLSConfig("")
			cfg.Certificates = nil
			cfg.GetClientCertificate = nil
			cfg.ClientSessionCache = nil
			cfg.InsecureSkipVerify = false
			var fallback http.RoundTripper
			if auto {
				fallback = c.discoveryTransport
			}
			c.discoveryHTTP3 = newAPIHTTP3Transport(c, template, cfg, fallback, delay)
		}
		transport = c.discoveryHTTP3
	}
	return &http.Client{Transport: transport, Timeout: apiRequestTimeout, CheckRedirect: rejectAPIRedirect}, nil
}

func rejectAPIRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func (c *Client) apiTLSConfig(addr string) *tls.Config {
	cfg := &tls.Config{}
	if c.TLSClientConfig != nil {
		cfg = c.TLSClientConfig.Clone()
	}
	if tlsConfigHasClientCertificate(c.TLSClientConfig) {
		cfg.ServerName = ""
		if host, _, err := net.SplitHostPort(addr); err == nil {
			cfg.ServerName = host
		}
	}
	cfg.NextProtos = nil
	return cfg
}

func (c *Client) invalidateEngineDiscovery() {
	c.apiDiscovery.mu.Lock()
	c.apiDiscovery.expires = time.Time{}
	c.apiDiscovery.mu.Unlock()
}
