// See LICENSE file in the project root for license information.

package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

func writeTestConfig(t *testing.T, cfg Config) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := WriteAtomic(path, cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func clearRstreamEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"RSTREAM_API_URL",
		"RSTREAM_AUTHENTICATION_TOKEN",
		"RSTREAM_CONFIG",
		"RSTREAM_CONTEXT",
		"RSTREAM_ENGINE",
		"RSTREAM_QUIC_TRANSPORT",
		"RSTREAM_TUNNEL_TRANSPORT",
	} {
		t.Setenv(key, "")
	}
}

func TestCreateTURNCredentialsFromEnvUsesPATDerivation(t *testing.T) {
	clearRstreamEnv(t)
	path := writeTestConfig(t, Config{
		Version: 1,
		Defaults: Defaults{
			Context: &DefaultContext{Name: "prod"},
		},
		Environments: []Environment{{
			APIURL: "https://rstream.io",
			Auth: &Auth{Token: &Token{Storage: &TokenStorage{
				Kind:  TokenStorageInline,
				Value: "eyJhbGciOiJIUzI1NiJ9.eyJ0eXBlIjoicGF0IiwidG9rZW5fZW5kcG9pbnQiOiJiOTVmYWY3ZiJ9.sig",
			}}},
		}},
		Contexts: []Context{{
			Name:            "prod",
			APIURL:          "https://rstream.io",
			ProjectEndpoint: "abc12345",
			Engine:          "abc12345.aws-eu-west-3-1.c.rstream.io:443",
			TURNDomain:      "aws-eu-west-3-1.c.rstream.io",
			TURNPort:        3478,
			TURNSPort:       5349,
		}},
	})
	res, err := CreateTURNCredentialsFromEnv(context.Background(), TURNCredentialsEnvOptions{
		ConfigPath: path,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if !strings.Contains(res.Username, ":pat:abc12345:b95faf7f") {
		t.Fatalf("unexpected username: %s", res.Username)
	}
	if len(res.URLs) != 4 || res.URLs[0] != "turn:aws-eu-west-3-1.c.rstream.io:3478?transport=udp" {
		t.Fatalf("unexpected urls: %+v", res.URLs)
	}
}

func TestCreateTURNCredentialsFromEnvFallsBackToAPIWithoutTURNContext(t *testing.T) {
	clearRstreamEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/api/projects/tunnels/resolve/abc12345/turn-server/credentials" {
			t.Fatalf("unexpected path: %s", got)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("missing authorization: %q", got)
		}
		if got := r.Header.Get("X-Deployment-Bypass"); got != "secret" {
			t.Fatalf("control plane header = %q", got)
		}
		var payload struct {
			TTLSeconds *int `json:"ttlSeconds,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.TTLSeconds == nil || *payload.TTLSeconds != 120 {
			t.Fatalf("ttlSeconds = %#v, want 120", payload.TTLSeconds)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rstream.TURNCredentials{
			Username:   "u",
			Credential: "c",
			URLs:       []string{"turn:example.com:3478?transport=udp"},
			TTL:        86400,
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()
	path := writeTestConfig(t, Config{
		Version: 1,
		Defaults: Defaults{
			Context: &DefaultContext{Name: "prod"},
		},
		Environments: []Environment{{
			APIURL:  server.URL,
			Headers: map[string]string{"X-Deployment-Bypass": "secret"},
			Auth: &Auth{Token: &Token{Storage: &TokenStorage{
				Kind:  TokenStorageInline,
				Value: "eyJhbGciOiJIUzI1NiJ9.eyJ0eXBlIjoicGF0IiwidG9rZW5fZW5kcG9pbnQiOiJiOTVmYWY3ZiJ9.sig",
			}}},
		}},
		Contexts: []Context{{
			Name:            "prod",
			APIURL:          server.URL,
			ProjectEndpoint: "abc12345",
			Engine:          "abc12345.aws-eu-west-3-1.c.rstream.io:443",
		}},
	})
	res, err := CreateTURNCredentialsFromEnv(context.Background(), TURNCredentialsEnvOptions{
		ConfigPath: path,
		TTL:        2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.Username != "u" || res.Credential != "c" {
		t.Fatalf("unexpected response: %+v", res)
	}
}
