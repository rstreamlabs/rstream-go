// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCreateRstreamLoginPostsTypedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/rstream/login/requests" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var req RstreamLoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if len(req.Permissions) != 1 || req.Permissions[0] != "account.projects.read-only" {
			http.Error(w, "unexpected permissions", http.StatusBadRequest)
			return
		}
		if len(req.Source) != 1 || req.Source[0].Key != "agent" || req.Source[0].Value != "rstream-cli" {
			http.Error(w, "unexpected source", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RstreamLoginResponse{
			RequestID:     "request-1",
			RequestSecret: "secret-1",
			URL:           "https://rstream.example.test/dashboard/rstream/login?request=request-1",
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	res, err := client.CreateRstreamLogin(context.Background(), RstreamLoginRequest{
		Permissions: []string{"account.projects.read-only"},
		Source:      []RstreamLabel{{Key: "agent", Value: "rstream-cli"}},
	})
	if err != nil {
		t.Fatalf("CreateRstreamLogin() error = %v", err)
	}
	if res.RequestID != "request-1" || res.RequestSecret != "secret-1" {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestExchangeRstreamLoginTokenEscapesRequestIDAndPostsSecret(t *testing.T) {
	requestID := "request/id with space"
	expectedPath := "/api/rstream/login/requests/" + url.PathEscape(requestID) + "/token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != expectedPath {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var req RstreamLoginTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if req.RequestSecret != "secret-1" {
			http.Error(w, "unexpected secret", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RstreamLoginTokenResponse{
			Status: "issued",
			Token:  "token-1",
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	res, err := client.ExchangeRstreamLoginToken(context.Background(), requestID, RstreamLoginTokenRequest{
		RequestSecret: "secret-1",
	})
	if err != nil {
		t.Fatalf("ExchangeRstreamLoginToken() error = %v", err)
	}
	if res.Status != "issued" || res.Token != "token-1" {
		t.Fatalf("unexpected response: %+v", res)
	}
}
