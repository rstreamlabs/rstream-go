// See LICENSE file in the project root for license information.

package httpcors_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/httpcors"
)

func TestPolicy(t *testing.T) {
	policy, err := httpcors.New(&httpcors.Config{AllowedOrigins: []string{"https://dashboard.example:443"}, AllowedMethods: []string{"GET", "HEAD", "PUT", "PROPFIND", "OPTIONS"}, AllowedHeaders: []string{"Authorization", "Content-Type", "Depth", "Range"}, ExposedHeaders: []string{"ETag", "Content-Range"}, MaxAge: 600})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, origin, requestedMethod, requestedHeaders string
		status, calls                                           int
		allow                                                   bool
	}{
		{name: "read", method: "GET", origin: "https://dashboard.example", status: 401, calls: 1, allow: true},
		{name: "no origin", method: "GET", status: 401, calls: 1},
		{name: "same origin", method: "DELETE", origin: "http://files.example", status: 401, calls: 1, allow: true},
		{name: "bearer preflight", method: "OPTIONS", origin: "https://dashboard.example", requestedMethod: "PUT", requestedHeaders: "authorization, content-type", status: 204, allow: true},
		{name: "webdav preflight", method: "OPTIONS", origin: "https://dashboard.example", requestedMethod: "PROPFIND", requestedHeaders: "DEPTH", status: 204, allow: true},
		{name: "ordinary options", method: "OPTIONS", origin: "https://dashboard.example", status: 401, calls: 1, allow: true},
		{name: "untrusted origin", method: "GET", origin: "https://untrusted.example", status: 403},
		{name: "opaque origin", method: "GET", origin: "null", status: 403},
		{name: "origin suffix", method: "GET", origin: "https://dashboard.example.evil.test", status: 403},
		{name: "untrusted preflight", method: "OPTIONS", origin: "https://untrusted.example", requestedMethod: "PUT", status: 403},
		{name: "unlisted method", method: "OPTIONS", origin: "https://dashboard.example", requestedMethod: "DELETE", status: 403, allow: true},
		{name: "unlisted header", method: "OPTIONS", origin: "https://dashboard.example", requestedMethod: "PUT", requestedHeaders: "x-secret", status: 403, allow: true},
		{name: "unlisted actual method", method: "DELETE", origin: "https://dashboard.example", status: 403, allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := policy.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header()["access-control-allow-credentials"] = []string{"true"}
				w.Header().Set("Vary", "Accept-Encoding")
				http.Error(w, "authentication required", http.StatusUnauthorized)
			}))
			req := httptest.NewRequest(tc.method, "http://files.example/fs/file", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.requestedMethod != "" {
				req.Header.Set("Access-Control-Request-Method", tc.requestedMethod)
			}
			if tc.requestedHeaders != "" {
				req.Header.Set("Access-Control-Request-Headers", tc.requestedHeaders)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.status || calls != tc.calls {
				t.Fatalf("status/calls = %d/%d, want %d/%d", res.Code, calls, tc.status, tc.calls)
			}
			want := ""
			if tc.allow {
				want = tc.origin
			}
			if res.Header().Get("Access-Control-Allow-Origin") != want || res.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatalf("unexpected CORS headers: %v", res.Header())
			}
			if !strings.Contains(strings.Join(res.Header().Values("Vary"), ","), "Origin") {
				t.Fatal("missing Vary: Origin")
			}
			if tc.status == 204 && (res.Header().Get("Access-Control-Max-Age") != "600" || !strings.Contains(res.Header().Get("Access-Control-Allow-Headers"), "Authorization")) {
				t.Fatalf("missing preflight headers: %v", res.Header())
			}
		})
	}
}

func TestDisabledAndEmptyResponses(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var config *httpcors.Config
		if enabled {
			config = &httpcors.Config{AllowedOrigins: []string{"https://dashboard.example"}, AllowCredentials: true}
		}
		policy, err := httpcors.New(config)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(policy.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		})))
		defer server.Close()
		req, err := http.NewRequest(http.MethodHead, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "https://dashboard.example")
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		want := "*"
		if enabled {
			want = "https://dashboard.example"
		}
		if res.Header.Get("Access-Control-Allow-Origin") != want {
			t.Fatalf("enabled=%v, headers=%v", enabled, res.Header)
		}
		if enabled && res.Header.Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatal("credentials not enabled")
		}
	}
}

func TestStreaming(t *testing.T) {
	policy, err := httpcors.New(&httpcors.Config{AllowedOrigins: []string{"https://dashboard.example"}})
	if err != nil {
		t.Fatal(err)
	}
	handler := policy.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, "stream"); err != nil {
			t.Fatal(err)
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://dashboard.example")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if !res.Flushed || res.Body.String() != "stream" {
		t.Fatal("response was not streamed")
	}
}

func TestConfigValidationAndOwnership(t *testing.T) {
	for _, origin := range []string{"*", "null", "https://*.example", "https://example/", "https://user@example", "https://example?x", "https://example#", "https://example:", "https://example:65536", "ftp://example", "https://example\r\n"} {
		if _, err := httpcors.New(&httpcors.Config{AllowedOrigins: []string{origin}}); err == nil {
			t.Errorf("accepted invalid origin %q", origin)
		}
	}
	for _, config := range []*httpcors.Config{
		{},
		{AllowedOrigins: []string{"https://example"}, MaxAge: -1},
		{AllowedOrigins: []string{"https://example"}, AllowedHeaders: []string{"*"}},
		{AllowedOrigins: []string{"https://example"}, AllowedMethods: []string{"TRACE"}},
		{AllowedOrigins: []string{"https://example"}, ExposedHeaders: []string{"bad\nheader"}},
	} {
		if _, err := httpcors.New(config); err == nil {
			t.Errorf("accepted invalid config: %+v", config)
		}
	}
	config := &httpcors.Config{AllowedOrigins: []string{"https://EXAMPLE:443"}, AllowedMethods: []string{"get"}}
	normalized, err := httpcors.Normalize(config)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.AllowedOrigins[0] != "https://example" || normalized.AllowedMethods[0] != "GET" || config.AllowedMethods[0] != "get" || config.AllowedOrigins[0] != "https://EXAMPLE:443" {
		t.Fatal("normalization mutated the supplied configuration")
	}
}
