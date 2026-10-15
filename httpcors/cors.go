// See LICENSE file in the project root for license information.

// Package httpcors implements an explicit, bounded HTTP CORS policy.
package httpcors

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

type Config struct {
	AllowedOrigins   []string `json:"allowed_origins" yaml:"allowedOrigins"`
	AllowedMethods   []string `json:"allowed_methods,omitempty" yaml:"allowedMethods,omitempty"`
	AllowedHeaders   []string `json:"allowed_headers,omitempty" yaml:"allowedHeaders,omitempty"`
	ExposedHeaders   []string `json:"exposed_headers,omitempty" yaml:"exposedHeaders,omitempty"`
	AllowCredentials bool     `json:"allow_credentials,omitempty" yaml:"allowCredentials,omitempty"`
	MaxAge           int64    `json:"max_age,omitempty" yaml:"maxAge,omitempty"`
}

func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	copy := *c
	copy.AllowedOrigins = slices.Clone(c.AllowedOrigins)
	copy.AllowedMethods = slices.Clone(c.AllowedMethods)
	copy.AllowedHeaders = slices.Clone(c.AllowedHeaders)
	copy.ExposedHeaders = slices.Clone(c.ExposedHeaders)
	return &copy
}

// Normalize validates and copies a policy; nil preserves application-owned CORS.
func Normalize(c *Config) (*Config, error) {
	if c == nil {
		return nil, nil
	}
	c = c.Clone()
	if len(c.AllowedOrigins) == 0 {
		return nil, fmt.Errorf("CORS requires at least one allowed origin")
	}
	if c.MaxAge < 0 || c.MaxAge > 86400 {
		return nil, fmt.Errorf("CORS max age must be between 0 and 86400 seconds")
	}
	if len(c.AllowedMethods) == 0 {
		c.AllowedMethods = []string{"GET", "HEAD", "POST"}
	}
	if len(c.AllowedHeaders) == 0 {
		c.AllowedHeaders = []string{"Authorization", "Content-Type"}
	}
	size := 0
	for group, values := range [][]string{c.AllowedOrigins, c.AllowedMethods, c.AllowedHeaders, c.ExposedHeaders} {
		if len(values) > 128 {
			return nil, fmt.Errorf("CORS lists cannot exceed 128 entries")
		}
		for i, value := range values {
			size += len(value)
			if size > 16384 {
				return nil, fmt.Errorf("CORS configuration exceeds 16384 bytes")
			}
			if group == 0 {
				origin, err := normalizeOrigin(value)
				if err != nil {
					return nil, err
				}
				values[i] = origin
				continue
			}
			if value == "*" || !httpguts.ValidHeaderFieldName(value) {
				return nil, fmt.Errorf("CORS methods and headers must be explicit HTTP tokens")
			}
			if group == 1 {
				values[i] = strings.ToUpper(value)
				if values[i] == "CONNECT" || values[i] == "TRACE" || values[i] == "TRACK" {
					return nil, fmt.Errorf("CORS does not support method %s", values[i])
				}
			} else {
				values[i] = http.CanonicalHeaderKey(value)
			}
		}
	}
	return c, nil
}

func normalizeOrigin(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(value, "*\\\r\n\t #?") {
		return "", fmt.Errorf("CORS origin must be an explicit HTTP(S) origin without a path, credentials, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("CORS origin port must be between 1 and 65535")
		}
		if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
			u.Host = strings.TrimSuffix(u.Host, ":"+port)
		}
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

type Policy struct {
	origins       map[string]bool
	methods       map[string]bool
	headers       map[string]bool
	allowMethods  string
	allowHeaders  string
	exposeHeaders string
	credentials   bool
	maxAge        string
}

func New(c *Config) (*Policy, error) {
	c, err := Normalize(c)
	if err != nil || c == nil {
		return nil, err
	}
	p := &Policy{origins: make(map[string]bool), methods: make(map[string]bool), headers: make(map[string]bool), allowMethods: strings.Join(c.AllowedMethods, ", "), allowHeaders: strings.Join(c.AllowedHeaders, ", "), exposeHeaders: strings.Join(c.ExposedHeaders, ", "), credentials: c.AllowCredentials, maxAge: strconv.FormatInt(c.MaxAge, 10)}
	for _, origin := range c.AllowedOrigins {
		p.origins[origin] = true
	}
	for _, method := range c.AllowedMethods {
		p.methods[method] = true
	}
	for _, header := range c.AllowedHeaders {
		p.headers[strings.ToLower(header)] = true
	}
	return p, nil
}

// Prepare establishes response headers before authentication or other errors.
// Call Preflight only after the service's host and network admission checks.
func (p *Policy) Prepare(w http.ResponseWriter, r *http.Request, targetOrigin string) (http.ResponseWriter, bool) {
	if p == nil || r.Method == http.MethodConnect {
		return w, true
	}
	writer := &responseWriter{ResponseWriter: w, policy: p}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return writer, true
	}
	origin, err := normalizeOrigin(r.Header.Get("Origin"))
	targetOrigin, _ = normalizeOrigin(targetOrigin)
	sameOrigin := err == nil && origin == targetOrigin
	if len(origins) != 1 || err != nil || (!sameOrigin && !p.origins[origin]) {
		http.Error(writer, "Browser origin is not allowed", http.StatusForbidden)
		return writer, false
	}
	writer.origin = r.Header.Get("Origin")
	writer.preflight = r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
	if !sameOrigin && !writer.preflight && !p.methods[r.Method] {
		http.Error(writer, "Browser method is not allowed", http.StatusForbidden)
		return writer, false
	}
	return writer, true
}

// Preflight answers only CORS negotiation and never invokes an application.
func (p *Policy) Preflight(w http.ResponseWriter, r *http.Request) bool {
	if p == nil || r.Method != http.MethodOptions || r.Header.Get("Origin") == "" || r.Header.Get("Access-Control-Request-Method") == "" {
		return false
	}
	valid := len(r.Header.Values("Access-Control-Request-Method")) == 1 && p.methods[r.Header.Get("Access-Control-Request-Method")]
	for _, field := range r.Header.Values("Access-Control-Request-Headers") {
		for _, header := range strings.Split(field, ",") {
			header = strings.TrimSpace(header)
			valid = valid && httpguts.ValidHeaderFieldName(header) && p.headers[strings.ToLower(header)]
		}
	}
	if !valid {
		http.Error(w, "CORS preflight is not allowed", http.StatusForbidden)
		return true
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (p *Policy) Wrap(next http.Handler) http.Handler {
	if p == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		targetOrigin, _ := normalizeOrigin(scheme + "://" + r.Host)
		w, ok := p.Prepare(w, r, targetOrigin)
		if !ok || p.Preflight(w, r) {
			return
		}
		next.ServeHTTP(w, r)
		Finish(w)
	})
}

// Finish commits headers for handlers that return without writing a body.
func Finish(w http.ResponseWriter) {
	if writer, ok := w.(*responseWriter); ok {
		writer.WriteHeader(http.StatusOK)
	}
}

type responseWriter struct {
	http.ResponseWriter
	policy    *Policy
	origin    string
	preflight bool
	written   bool
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseWriter) WriteHeader(status int) {
	if w.written {
		return
	}
	h := w.Header()
	for name := range h {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			delete(h, name)
		}
	}
	addVary(h, "Origin")
	if w.preflight {
		addVary(h, "Access-Control-Request-Method")
		addVary(h, "Access-Control-Request-Headers")
	}
	if w.origin != "" {
		h.Set("Access-Control-Allow-Origin", w.origin)
		if w.policy.credentials {
			h.Set("Access-Control-Allow-Credentials", "true")
		}
		if w.preflight && status >= 200 && status < 300 {
			h.Set("Access-Control-Allow-Methods", w.policy.allowMethods)
			h.Set("Access-Control-Allow-Headers", w.policy.allowHeaders)
			h.Set("Access-Control-Max-Age", w.policy.maxAge)
		} else if !w.preflight && w.policy.exposeHeaders != "" {
			h.Set("Access-Control-Expose-Headers", w.policy.exposeHeaders)
		}
	}
	w.written = status >= 200 || status == http.StatusSwitchingProtocols
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.written = true
	}
	return conn, buffer, err
}

func (w *responseWriter) Flush() { _ = w.FlushError() }

func (w *responseWriter) FlushError() error {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func addVary(h http.Header, value string) {
	for _, field := range h.Values("Vary") {
		for _, token := range strings.Split(field, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) || strings.TrimSpace(token) == "*" {
				return
			}
		}
	}
	h.Add("Vary", value)
}
