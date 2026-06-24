// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

func TestMCPAuthStartAndPollStoresTokenWithoutReturningIt(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RSTREAM_CONFIG", configPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(controlplane.OAuthAuthorizationServerMetadata{DeviceAuthorizationEndpoint: "/oauth/device_authorization", TokenEndpoint: "/oauth/token"})
		case "/oauth/device_authorization":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm returned error: %v", err)
			}
			scope := r.Form.Get("scope")
			hasPlanRead := strings.Contains(scope, "account.plan.read-only")
			hasProjectWrite := strings.Contains(scope, "account.projects.read-write")
			hasTokenCreate := strings.Contains(scope, "account.tokens.create")
			hasWorkspaceRead := strings.Contains(scope, "account.workspaces.read-only")
			hasStreamRead := strings.Contains(scope, "network.streams.read-only")
			hasEventRead := strings.Contains(scope, "network.events.read-only")
			hasWebhookRead := strings.Contains(scope, "network.webhooks.read-only")
			hasWebhookWrite := strings.Contains(scope, "network.webhooks.read-write")
			hasWebTTYServerRead := strings.Contains(scope, "network.webtty-servers.read-only")
			hasWebTTYServerWrite := strings.Contains(scope, "network.webtty-servers.read-write")
			hasWebTTYSessionReadWrite := strings.Contains(scope, "webtty.sessions.read-write")
			hasWebTTYLogsRead := strings.Contains(scope, "webtty.logs.read-only")
			hasTunnelCreate := strings.Contains(scope, "tunnels.tunnels.create-delete")
			if r.Form.Get("client_id") != rstreamOAuthClientID || !hasPlanRead || !hasProjectWrite || !hasTokenCreate || !hasWorkspaceRead || !hasStreamRead || !hasEventRead || !hasWebhookRead || hasWebhookWrite || !hasWebTTYServerRead || hasWebTTYServerWrite || hasWebTTYSessionReadWrite || hasWebTTYLogsRead || !hasTunnelCreate {
				t.Fatalf("unexpected device authorization form: %s", r.Form.Encode())
			}
			_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceAuthorizationResponse{DeviceCode: "device-code", UserCode: "USER-CODE", VerificationURI: serverURL(r, "/activate"), VerificationURIComplete: serverURL(r, "/activate?user_code=USER-CODE"), ExpiresIn: 60, Interval: 1})
		case "/oauth/token":
			body := readOAuthTestForm(t, r)
			if body.Get("grant_type") != controlplane.OAuthDeviceGrantType || body.Get("device_code") != "device-code" || body.Get("client_id") != rstreamOAuthClientID {
				t.Fatalf("unexpected token form: %s", body.Encode())
			}
			_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceTokenResponse{AccessToken: "approved-token", TokenType: "Bearer"})
		case "/api/whoami":
			if r.Header.Get("Authorization") != "Bearer approved-token" {
				t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(controlplane.Whoami{ID: "user", Role: "admin"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	start, err := mcpAuthStart(t.Context(), map[string]json.RawMessage{"api_url": json.RawMessage(strconvQuote(server.URL))})
	if err != nil {
		t.Fatalf("mcpAuthStart returned error: %v", err)
	}
	startText := mcpResultText(t, start)
	if strings.Contains(startText, "device-code") || strings.Contains(startText, "approved-token") || strings.Contains(startText, `"user_code"`) || !strings.Contains(startText, "user_code=USER-CODE") {
		t.Fatalf("unexpected start response: %s", startText)
	}
	var startPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(startText), &startPayload); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	poll, err := mcpAuthPoll(t.Context(), map[string]json.RawMessage{"id": json.RawMessage(strconvQuote(startPayload.ID))})
	if err != nil {
		t.Fatalf("mcpAuthPoll returned error: %v", err)
	}
	pollText := mcpResultText(t, poll)
	if strings.Contains(pollText, "approved-token") || !strings.Contains(pollText, `"authenticated": true`) {
		t.Fatalf("unexpected poll response: %s", pollText)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	env, _ := cfg.FindEnvironment(server.URL)
	if env == nil {
		t.Fatalf("stored environment not found")
	}
	token, ok, err := config.TokenFromAuth(env.Auth)
	if err != nil || !ok || token != "approved-token" {
		t.Fatalf("stored token = %q ok=%v err=%v", token, ok, err)
	}
	status, err := mcpRuntimeStatus()
	if err != nil {
		t.Fatalf("mcpRuntimeStatus returned error: %v", err)
	}
	statusText := mcpResultText(t, status)
	if !strings.Contains(statusText, `"api_url": "`+server.URL+`"`) || !strings.Contains(statusText, `"has_token": true`) {
		t.Fatalf("unexpected runtime status after auth: %s", statusText)
	}
	sessions, err := readMCPAuthRegistry(mcpAuthRegistryPath(configPath))
	if err != nil || len(sessions) != 0 {
		t.Fatalf("auth sessions after success = %#v err=%v", sessions, err)
	}
}

func TestMCPRuntimeStatusRequiresTokenToBeReady(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RSTREAM_CONFIG", configPath)
	status, err := mcpRuntimeStatus()
	if err != nil {
		t.Fatalf("mcpRuntimeStatus returned error: %v", err)
	}
	statusText := mcpResultText(t, status)
	if !strings.Contains(statusText, `"ready": false`) || !strings.Contains(statusText, `"needs_login": true`) || !strings.Contains(statusText, `"has_token": false`) || !strings.Contains(statusText, `"suggested_next_tool": "rstream_auth_start"`) {
		t.Fatalf("unexpected clean runtime status: %s", statusText)
	}
}

func TestMCPAuthStartCanRequestExplicitPermissions(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RSTREAM_CONFIG", configPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(controlplane.OAuthAuthorizationServerMetadata{DeviceAuthorizationEndpoint: "/oauth/device_authorization", TokenEndpoint: "/oauth/token"})
		case "/oauth/device_authorization":
			form := readOAuthTestForm(t, r)
			scope := form.Get("scope")
			if !strings.Contains(scope, "account.projects.read-write") || !strings.Contains(scope, "account.plan.read-only") || !strings.Contains(scope, "tunnels.tunnels.create-delete") || strings.Contains(scope, "account.projects.read-only") {
				t.Fatalf("unexpected explicit scope: %q", form.Get("scope"))
			}
			_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceAuthorizationResponse{DeviceCode: "device-code", UserCode: "USER-CODE", VerificationURI: serverURL(r, "/activate"), ExpiresIn: 60, Interval: 1})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	start, err := mcpAuthStart(t.Context(), map[string]json.RawMessage{"api_url": json.RawMessage(strconvQuote(server.URL)), "permissions": json.RawMessage(`["account.projects.read-write","account.plan.read-only"]`)})
	if err != nil {
		t.Fatalf("mcpAuthStart returned error: %v", err)
	}
	text := mcpResultText(t, start)
	if !strings.Contains(text, "account.projects.read-write") || !strings.Contains(text, "account.plan.read-only") || !strings.Contains(text, "tunnels.tunnels.create-delete") {
		t.Fatalf("start response did not include explicit scopes: %s", text)
	}
}

func TestMCPAuthStartUsesSingleConfiguredEnvironmentAPIURL(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	requestedDeviceAuthorization := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(controlplane.OAuthAuthorizationServerMetadata{DeviceAuthorizationEndpoint: "/oauth/device_authorization", TokenEndpoint: "/oauth/token"})
		case "/oauth/device_authorization":
			requestedDeviceAuthorization = true
			_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceAuthorizationResponse{DeviceCode: "device-code", UserCode: "USER-CODE", VerificationURI: serverURL(r, "/activate"), VerificationURIComplete: serverURL(r, "/activate?user_code=USER-CODE"), ExpiresIn: 60, Interval: 1})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if err := os.WriteFile(configPath, []byte(`version: 1
environments:
  - apiUrl: `+server.URL+`
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("RSTREAM_CONFIG", configPath)
	start, err := mcpAuthStart(t.Context(), map[string]json.RawMessage{})
	if err != nil {
		t.Fatalf("mcpAuthStart returned error: %v", err)
	}
	if !requestedDeviceAuthorization {
		t.Fatalf("mcpAuthStart did not use the configured environment API URL")
	}
	text := mcpResultText(t, start)
	if !strings.Contains(text, `"api_url": "`+server.URL+`"`) || !strings.Contains(text, server.URL+`/activate?user_code=USER-CODE`) {
		t.Fatalf("unexpected auth start response: %s", text)
	}
}

func TestMCPAuthPollPendingAndSlowDownDoNotStoreToken(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RSTREAM_CONFIG", configPath)
	tokenCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(controlplane.OAuthAuthorizationServerMetadata{DeviceAuthorizationEndpoint: "/oauth/device_authorization", TokenEndpoint: "/oauth/token"})
		case "/oauth/device_authorization":
			_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceAuthorizationResponse{DeviceCode: "device-code", UserCode: "USER-CODE", VerificationURI: serverURL(r, "/activate"), ExpiresIn: 60, Interval: 1})
		case "/oauth/token":
			tokenCalls++
			w.WriteHeader(http.StatusBadRequest)
			if tokenCalls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	start, err := mcpAuthStart(t.Context(), map[string]json.RawMessage{"api_url": json.RawMessage(strconvQuote(server.URL))})
	if err != nil {
		t.Fatalf("mcpAuthStart returned error: %v", err)
	}
	var startPayload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(mcpResultText(t, start)), &startPayload); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	pending, err := mcpAuthPoll(t.Context(), map[string]json.RawMessage{"id": json.RawMessage(strconvQuote(startPayload.ID))})
	if err != nil {
		t.Fatalf("pending poll returned error: %v", err)
	}
	if text := mcpResultText(t, pending); !strings.Contains(text, `"status": "pending"`) || !strings.Contains(text, `"next_poll_after_seconds": 1`) {
		t.Fatalf("unexpected pending response: %s", text)
	}
	slowDown, err := mcpAuthPoll(t.Context(), map[string]json.RawMessage{"id": json.RawMessage(strconvQuote(startPayload.ID))})
	if err != nil {
		t.Fatalf("slow_down poll returned error: %v", err)
	}
	if text := mcpResultText(t, slowDown); !strings.Contains(text, `"next_poll_after_seconds": 6`) {
		t.Fatalf("unexpected slow_down response: %s", text)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if env, _ := cfg.FindEnvironment(server.URL); env != nil && env.Auth != nil {
		t.Fatalf("token should not be stored while pending: %#v", env.Auth)
	}
}

func readOAuthTestForm(t *testing.T, r *http.Request) url.Values {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm returned error: %v", err)
	}
	return r.Form
}

func mcpResultText(t *testing.T, result map[string]any) string {
	t.Helper()
	content, ok := result["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("unexpected MCP result content: %#v", result["content"])
	}
	text, ok := content[0]["text"].(string)
	if !ok {
		t.Fatalf("unexpected MCP text content: %#v", content[0])
	}
	return text
}

func serverURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

func strconvQuote(value string) string {
	payload, _ := json.Marshal(value)
	return string(payload)
}
