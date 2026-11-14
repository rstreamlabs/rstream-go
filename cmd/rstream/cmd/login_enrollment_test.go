// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

func TestBrowserLoginNegotiatesOwnerSupportAndReportsEffectiveIdentity(t *testing.T) {
	for _, tc := range []struct {
		name           string
		flow           string
		versions       []int
		metadataStatus int
		wantVersion    int
		wantError      bool
	}{
		{name: "OAuth workspace", flow: loginAuthFlowOAuth, versions: []int{1, 2}, wantVersion: 1},
		{name: "OAuth old server", flow: loginAuthFlowOAuth},
		{name: "OAuth future version only", flow: loginAuthFlowOAuth, versions: []int{2}},
		{name: "JSON workspace", flow: loginAuthFlowLegacy, versions: []int{1}, wantVersion: 1},
		{name: "JSON old discovery", flow: loginAuthFlowLegacy},
		{name: "JSON without discovery", flow: loginAuthFlowLegacy, metadataStatus: http.StatusNotFound},
		{name: "JSON forbidden discovery fails closed", flow: loginAuthFlowLegacy, metadataStatus: http.StatusForbidden, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir()) // Never launch a real browser in a test.
			var requests, validations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/oauth-authorization-server":
					if tc.metadataStatus != 0 {
						w.WriteHeader(tc.metadataStatus)
						_ = json.NewEncoder(w).Encode(map[string]string{"error": "discovery unavailable"})
						return
					}
					_ = json.NewEncoder(w).Encode(controlplane.OAuthAuthorizationServerMetadata{DeviceAuthorizationEndpoint: "/oauth/device_authorization", TokenEndpoint: "/oauth/token", CredentialOwnerVersionsSupported: tc.versions})
				case "/oauth/device_authorization":
					requests.Add(1)
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if tc.wantVersion == 1 {
						if r.Form.Get("rstream_credential_owner_version") != "1" || r.Form.Has("scope") {
							t.Errorf("new server request duplicates a preset or lacks negotiation: %v", r.Form)
						}
					} else if r.Form.Has("rstream_credential_owner_version") || r.Form.Get("scope") != strings.Join(rstreamLoginPermissions, " ") {
						t.Errorf("legacy OAuth request changed: %v", r.Form)
					}
					_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceAuthorizationResponse{DeviceCode: "device", UserCode: "CODE", VerificationURI: serverURL(r, "/approve"), ExpiresIn: 60})
				case "/api/rstream/login/requests":
					requests.Add(1)
					var req controlplane.RstreamLoginRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.CredentialOwnerVersion != tc.wantVersion || (tc.wantVersion == 1 && req.Permissions != nil) || (tc.wantVersion == 0 && !reflect.DeepEqual(req.Permissions, rstreamLoginPermissions)) {
						t.Errorf("incorrect JSON enrollment request: %+v", req)
					}
					_ = json.NewEncoder(w).Encode(controlplane.RstreamLoginResponse{RequestID: "request", RequestSecret: "secret", URL: serverURL(r, "/approve"), CredentialOwnerVersion: tc.wantVersion})
				case "/oauth/token":
					_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceTokenResponse{AccessToken: "issued-token", TokenType: "Bearer", Scope: "tunnels.tunnels.create-delete"})
				case "/api/rstream/login/requests/request/token":
					_ = json.NewEncoder(w).Encode(controlplane.RstreamLoginTokenResponse{Status: "issued", Token: "issued-token"})
				case "/api/whoami":
					validations.Add(1)
					if r.URL.Query().Get("format") != "identity" || r.Header.Get("Authorization") != "Bearer issued-token" {
						t.Error("token was not verified through owner-aware identity")
					}
					if tc.wantVersion == 1 {
						_, _ = w.Write([]byte(`{"owner":{"kind":"workspace","workspaceId":"workspace-1"},"credentialId":"credential-1","permissions":["tunnels.tunnels.create-delete"],"resources":{"tunnels":{"projects":["project-1"]}}}`))
					} else {
						_, _ = w.Write([]byte(`{"id":"legacy-user","role":"user"}`))
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cmd := loginTokenStorageCommand()
			cmd.SetContext(t.Context())
			cmd.Flags().String("output", "json", "")
			path := filepath.Join(t.TempDir(), "config.yaml")
			out, err := captureStdout(t, func() error {
				if tc.flow == loginAuthFlowLegacy {
					return runLegacyDeviceLogin(cmd, path, config.Config{}, server.URL)
				}
				return runOAuthDeviceLogin(cmd, path, config.Config{}, server.URL)
			})
			if tc.wantError {
				if err == nil || requests.Load() != 0 || validations.Load() != 0 {
					t.Fatalf("failed discovery still started login: requests=%d validations=%d err=%v", requests.Load(), validations.Load(), err)
				}
				if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("failed login wrote config: %v", statErr)
				}
				return
			}
			if err != nil || requests.Load() != 1 || validations.Load() != 1 || strings.Contains(out, "issued-token") {
				t.Fatalf("login requests=%d validations=%d output=%q error=%v", requests.Load(), validations.Load(), out, err)
			}
			var result loginResult
			if err := json.Unmarshal([]byte(out), &result); err != nil || !result.Authenticated || result.Identity == nil {
				t.Fatalf("invalid login result: %q %v", out, err)
			}
			wantOwner := "user"
			if tc.wantVersion == 1 {
				wantOwner = "workspace"
				if result.Identity.Permissions == nil || !reflect.DeepEqual(*result.Identity.Permissions, []string{"tunnels.tunnels.create-delete"}) || !strings.Contains(string(result.Identity.Resources), "project-1") {
					t.Fatalf("result does not report the actual reduced scope: %+v", result.Identity)
				}
			}
			if result.Identity.Owner.Kind != wantOwner {
				t.Fatalf("incorrect owner: %+v", result.Identity)
			}
			loaded, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			environment, _ := loaded.FindEnvironment(server.URL)
			token, ok, err := config.TokenFromAuth(environment.Auth)
			if err != nil || !ok || token != "issued-token" {
				t.Fatalf("issued token was not persisted: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestLoginKeychainStorageKeepsPreviousProofsAndContexts(t *testing.T) {
	role := "user"
	identities := map[string]controlplane.CallerIdentity{
		"personal-token":          {Owner: controlplane.CredentialOwner{Kind: "user", UserID: "same-id"}, CredentialID: "personal", Role: &role},
		"workspace-token":         {Owner: controlplane.CredentialOwner{Kind: "workspace", WorkspaceID: "same-id"}, CredentialID: "workspace", Permissions: new([]string{})},
		"workspace-rotated-token": {Owner: controlplane.CredentialOwner{Kind: "workspace", WorkspaceID: "same-id"}, CredentialID: "workspace", Permissions: new([]string{})},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(identities[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")])
	}))
	defer server.Close()
	previous := config.NewMacOSKeychainTokenStorage(server.URL)
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{Contexts: []config.Context{{Name: "existing", APIURL: server.URL, Auth: &config.Auth{Token: &config.Token{Storage: &previous}}}}}
	if err := config.UpdateAtomic(path, func(latest *config.Config) error { *latest = cfg; return nil }); err != nil {
		t.Fatal(err)
	}
	provider := storeTokenInCredentialProvider
	t.Cleanup(func() { storeTokenInCredentialProvider = provider })
	slots := map[string]string{}
	storeTokenInCredentialProvider = func(storage config.TokenStorage, token string) error {
		if storage.Account == previous.Account || strings.Contains(storage.Account, token) {
			t.Error("keychain account overwrites the legacy slot or leaks the raw token")
		}
		if _, exists := slots[storage.Account]; exists {
			t.Error("a different approved proof reused an existing keychain slot")
		}
		slots[storage.Account] = token
		return nil
	}
	for _, token := range []string{"personal-token", "workspace-token", "workspace-rotated-token"} {
		identity, err := storeTokenAndIdentity(t.Context(), path, cfg, server.URL, token, previous)
		if err != nil || identity.Owner != identities[token].Owner {
			t.Fatalf("store identity=%+v err=%v", identity, err)
		}
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Contexts[0].Auth.Token.Storage.Account != previous.Account || len(slots) != 3 {
		t.Fatal("login changed an existing context or reused a slot")
	}
	environment, _ := loaded.FindEnvironment(server.URL)
	last := *environment.Auth.Token.Storage
	if slots[last.Account] != "workspace-rotated-token" || last.Value != "" {
		t.Fatalf("config does not reference the last approved proof: %+v", last)
	}
	storeTokenInCredentialProvider = func(config.TokenStorage, string) error { return errors.New("provider unavailable") }
	if _, err := storeTokenAndIdentity(t.Context(), path, cfg, server.URL, "workspace-token", previous); err == nil {
		t.Fatal("failed keychain storage was accepted")
	}
	loaded, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	environment, _ = loaded.FindEnvironment(server.URL)
	if !reflect.DeepEqual(*environment.Auth.Token.Storage, last) {
		t.Fatal("failed keychain write changed the active token")
	}
}

func TestNativeOAuthEnrollmentNeverBroadensAnExplicitScope(t *testing.T) {
	metadata := controlplane.OAuthAuthorizationServerMetadata{CredentialOwnerVersionsSupported: []int{1}}
	request, err := nativeOAuthDeviceLoginRequest(metadata, []string{"account.projects.read-only"})
	if err != nil || request.CredentialOwnerVersion != 1 || request.Scope != "account.projects.read-only" {
		t.Fatalf("explicit scope changed: %+v", request)
	}
	if _, err := nativeOAuthDeviceLoginRequest(metadata, []string{}); err == nil {
		t.Fatal("an explicitly empty scope would fall back to the broader server default")
	}
}

func TestMCPNativeEnrollmentStoresWorkspaceTokenAndReportsItsActualScope(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RSTREAM_CONFIG", configPath)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(controlplane.OAuthAuthorizationServerMetadata{DeviceAuthorizationEndpoint: "/oauth/device_authorization", TokenEndpoint: "/oauth/token", CredentialOwnerVersionsSupported: []int{1}})
		case "/oauth/device_authorization":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("rstream_credential_owner_version") != "1" || r.Form.Get("scope") != strings.Join(rstreamMCPLoginPermissions, " ") {
				t.Errorf("MCP requested authority changed: %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceAuthorizationResponse{DeviceCode: "device", UserCode: "CODE", VerificationURI: serverURL(r, "/approve"), ExpiresIn: 60})
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(controlplane.OAuthDeviceTokenResponse{AccessToken: "mcp-workspace-token", TokenType: "Bearer", Scope: "account.projects.read-only"})
		case "/api/whoami":
			_, _ = w.Write([]byte(`{"owner":{"kind":"workspace","workspaceId":"workspace-1"},"credentialId":"credential-1","permissions":["account.projects.read-only"],"resources":{"tunnels":{"projects":["approved-project"]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	start, err := mcpAuthStart(t.Context(), map[string]json.RawMessage{"api_url": json.RawMessage(strconvQuote(server.URL))})
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(mcpResultText(t, start)), &session); err != nil {
		t.Fatal(err)
	}
	result, err := mcpAuthPoll(t.Context(), map[string]json.RawMessage{"id": json.RawMessage(strconvQuote(session.ID))})
	if err != nil {
		t.Fatal(err)
	}
	text := mcpResultText(t, result)
	if strings.Contains(text, "mcp-workspace-token") || !strings.Contains(text, "workspace-1") || !strings.Contains(text, "approved-project") || strings.Contains(text, "account.projects.read-write") {
		t.Fatalf("MCP result leaked the token or reported requested rather than effective access: %s", text)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	environment, _ := loaded.FindEnvironment(server.URL)
	token, ok, err := config.TokenFromAuth(environment.Auth)
	if err != nil || !ok || token != "mcp-workspace-token" {
		t.Fatalf("workspace token was not stored: ok=%v err=%v", ok, err)
	}
}
