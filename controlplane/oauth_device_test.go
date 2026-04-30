// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOAuthDeviceAuthorizationUsesFormEncoding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/device_authorization" {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			http.Error(w, "unexpected content type", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("client_id") != "rstream-cli" || r.Form.Get("scope") != "account.projects.read-only" {
			http.Error(w, "unexpected form values", http.StatusBadRequest)
			return
		}
		if r.Form.Get("rstream_source") == "" {
			http.Error(w, "missing source", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OAuthDeviceAuthorizationResponse{
			DeviceCode:      "device",
			UserCode:        "ABCD-EFGH",
			VerificationURI: "https://example.com/dashboard/rstream/login",
			ExpiresIn:       600,
			Interval:        5,
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "")
	res, err := client.CreateOAuthDeviceAuthorization(context.Background(), "/oauth/device_authorization", OAuthDeviceAuthorizationRequest{
		ClientID: "rstream-cli",
		Scope:    "account.projects.read-only",
		Source:   []RstreamLabel{{Key: "agent", Value: "rstream", Label: "Agent"}},
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if res.DeviceCode != "device" || res.UserCode != "ABCD-EFGH" {
		t.Fatalf("unexpected response: %+v", res)
	}
}

func TestOAuthDeviceTokenReturnsTypedOAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "authorization_pending",
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "")
	_, err := client.ExchangeOAuthDeviceToken(context.Background(), "/oauth/token", OAuthDeviceTokenRequest{
		ClientID:   "rstream-cli",
		DeviceCode: "device",
	})
	var oauthErr *OAuthError
	if !errors.As(err, &oauthErr) {
		t.Fatalf("expected OAuthError, got %T", err)
	}
	if oauthErr.Code != "authorization_pending" {
		t.Fatalf("unexpected OAuth error: %+v", oauthErr)
	}
}
