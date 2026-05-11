// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/controlplane"
)

func TestValidateToken(t *testing.T) {
	if err := validateToken(t.Context(), "https://api.example.com", " "); err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("expected missing token error, got %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/whoami" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad token"})
			return
		}
		_ = json.NewEncoder(w).Encode(controlplane.Whoami{ID: "user", Role: "admin"})
	}))
	defer server.Close()
	if err := validateToken(t.Context(), server.URL, "bad-token"); err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("expected unauthorized validation error, got %v", err)
	}
	if err := validateToken(t.Context(), server.URL, "good-token"); err != nil {
		t.Fatalf("validateToken(good-token) error = %v", err)
	}
}
