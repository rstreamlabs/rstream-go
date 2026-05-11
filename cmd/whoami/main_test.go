// See LICENSE file in the project root for license information.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWhoamiHandlerOmitsHeadersByDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	whoamiHandler(false).ServeHTTP(rec, req)
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Headers != nil {
		t.Fatalf("headers should be omitted by default: %#v", resp.Headers)
	}
}

func TestWhoamiHandlerRedactsSensitiveHeadersWhenEnabled(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cf-Access-Jwt-Assertion", "jwt")
	req.Header.Set("User-Agent", "agent")
	req.Header.Set("X-Amzn-Oidc-Data", "oidc")
	req.Header.Set("X-Auth-Key", "key")
	req.Header.Set("X-Trace-ID", "trace")
	rec := httptest.NewRecorder()
	whoamiHandler(true).ServeHTTP(rec, req)
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	for _, header := range []string{"Cf-Access-Jwt-Assertion", "User-Agent", "X-Amzn-Oidc-Data", "X-Auth-Key"} {
		if got := resp.Headers[header]; len(got) != 1 || got[0] != "<redacted>" {
			t.Fatalf("%s = %#v, want redacted", header, got)
		}
	}
	if got := resp.Headers["X-Trace-Id"]; len(got) != 1 || got[0] != "trace" {
		t.Fatalf("X-Trace-Id = %#v, want trace", got)
	}
}
