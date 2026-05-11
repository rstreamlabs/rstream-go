// See LICENSE file in the project root for license information.

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestOAuthHelpersNormalizeErrorsScopesAndRevocationForms(t *testing.T) {
	if got := (&OAuthError{Code: "slow_down", Description: "poll less often"}).Error(); got != "slow_down: poll less often" {
		t.Fatalf("OAuthError with description = %q", got)
	}
	if got := (&OAuthError{Code: "invalid_grant"}).Error(); got != "invalid_grant" {
		t.Fatalf("OAuthError without description = %q", got)
	}
	values, err := (OAuthDeviceAuthorizationRequest{
		ClientID: "rstream-cli",
		Scope:    " account.projects.read-only ",
		Source:   []RstreamLabel{{Key: "agent", Value: "rstream", Label: "CLI"}},
	}).values()
	if err != nil {
		t.Fatalf("values() error = %v", err)
	}
	if values.Get("scope") != "account.projects.read-only" {
		t.Fatalf("scope = %q, want trimmed scope", values.Get("scope"))
	}
	if got := values.Get("rstream_source"); !strings.Contains(got, `"key":"agent"`) || !strings.Contains(got, `"label":"CLI"`) {
		t.Fatalf("unexpected source payload: %s", got)
	}
	values, err = (OAuthDeviceAuthorizationRequest{ClientID: "rstream-cli", Scope: "  "}).values()
	if err != nil {
		t.Fatalf("blank scope values() error = %v", err)
	}
	if _, ok := values["scope"]; ok {
		t.Fatalf("blank scope should be omitted: %v", values)
	}
	revocation := tokenRevocationValues("token-1")
	if revocation.Get("token") != "token-1" || revocation.Get("token_type_hint") != "access_token" {
		t.Fatalf("unexpected revocation values: %v", revocation)
	}
}

func TestOAuthMetadataAndRevocationUseExpectedEndpoints(t *testing.T) {
	var revoked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			if r.Method != http.MethodGet {
				http.Error(w, "unexpected method", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(OAuthAuthorizationServerMetadata{
				Issuer:                      "https://issuer.example.test",
				DeviceAuthorizationEndpoint: "/oauth/device_authorization",
				TokenEndpoint:               "/oauth/token",
				RevocationEndpoint:          "/oauth/revoke",
				GrantTypesSupported:         []string{OAuthDeviceGrantType},
				ScopesSupported:             []string{"account.projects.read-only"},
			})
		case "/oauth/revoke":
			if r.Method != http.MethodPost {
				http.Error(w, "unexpected method", http.StatusBadRequest)
				return
			}
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form", http.StatusBadRequest)
				return
			}
			if r.Form.Get("token") != "token-1" || r.Form.Get("token_type_hint") != "access_token" {
				http.Error(w, "unexpected revocation form", http.StatusBadRequest)
				return
			}
			revoked = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "")
	metadata, err := client.OAuthAuthorizationServerMetadata(context.Background())
	if err != nil {
		t.Fatalf("metadata failed: %v", err)
	}
	if metadata.Issuer != "https://issuer.example.test" || metadata.TokenEndpoint != "/oauth/token" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	if err := client.RevokeOAuthToken(context.Background(), "/oauth/revoke", "token-1"); err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	if !revoked {
		t.Fatalf("revoke endpoint not called")
	}
}
