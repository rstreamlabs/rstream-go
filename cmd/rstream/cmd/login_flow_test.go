// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

func TestStoreTokenValidatesAndPersistsEnvironmentToken(t *testing.T) {
	apiToken := "validated-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/whoami" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+apiToken {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(controlplane.Whoami{ID: "user", Role: "admin"})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{}
	if err := storeToken(t.Context(), path, cfg, server.URL, apiToken); err != nil {
		t.Fatalf("storeToken() error = %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	env, _ := loaded.FindEnvironment(server.URL)
	token, ok, err := config.TokenFromAuth(env.Auth)
	if err != nil || !ok || token != apiToken {
		t.Fatalf("stored token = %q ok=%v err=%v", token, ok, err)
	}
}

func TestStoreTokenConcurrentUpdatesPreserveBothEnvironments(t *testing.T) {
	requests := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/whoami" {
			http.NotFound(w, r)
			return
		}
		requests <- struct{}{}
		<-release
		_ = json.NewEncoder(w).Encode(controlplane.Whoami{ID: "user", Role: "admin"})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	apiURLs := []string{server.URL, strings.Replace(server.URL, "127.0.0.1", "localhost", 1)}
	errors := make(chan error, len(apiURLs))
	var workers sync.WaitGroup
	for index, apiURL := range apiURLs {
		index, apiURL := index, apiURL
		workers.Add(1)
		go func() {
			defer workers.Done()
			errors <- storeToken(t.Context(), path, config.Config{}, apiURL, fmt.Sprintf("token-%d", index))
		}()
	}
	for range apiURLs {
		select {
		case <-requests:
		case <-time.After(time.Second):
			t.Fatal("concurrent token validation did not reach the server")
		}
	}
	close(release)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("storeToken returned error: %v", err)
		}
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	for index, apiURL := range apiURLs {
		environment, _ := loaded.FindEnvironment(apiURL)
		if environment == nil {
			t.Fatalf("environment %s was lost", apiURL)
		}
		token, ok, tokenErr := config.TokenFromAuth(environment.Auth)
		if tokenErr != nil || !ok || token != fmt.Sprintf("token-%d", index) {
			t.Fatalf("environment %s token=%q ok=%v error=%v", apiURL, token, ok, tokenErr)
		}
	}
}

func TestRstreamLoginPermissionsMatchCliWorkflows(t *testing.T) {
	want := []string{
		"account.plan.read-only",
		"account.projects.read-write",
		"account.tokens.create",
		"account.workspaces.read-only",
		"account.workspace-protection.read-write",
		"network.streams.read-only",
		"network.events.read-only",
		"network.webhooks.read-only",
		"network.webtty-servers.read-write",
		"tunnels.resources.read-only",
		"tunnels.streams.create-delete",
		"tunnels.tunnels.create-delete",
		"webtty.sessions.read-write",
		"webtty.logs.read-only",
		"turn.credentials.create",
		"turn.relay.allocate",
	}
	if !slices.Equal(rstreamLoginPermissions, want) {
		t.Fatalf("rstreamLoginPermissions = %#v", rstreamLoginPermissions)
	}
}

func TestLoginTokenStorageFromFlags(t *testing.T) {
	command := loginTokenStorageCommand()
	storage, err := tokenStorageFromFlags(command, "https://api.example.com")
	if err != nil {
		t.Fatalf("tokenStorageFromFlags(inline) error = %v", err)
	}
	if storage.Kind != config.TokenStorageInline {
		t.Fatalf("inline storage = %#v", storage)
	}
	command = loginTokenStorageCommand()
	mustSetFlag(t, command, "token-storage", tokenStorageMacOSKeychain)
	storage, err = tokenStorageFromFlags(command, "https://api.example.com")
	if err != nil {
		t.Fatalf("tokenStorageFromFlags(keychain) error = %v", err)
	}
	if storage.Kind != config.TokenStorageKeychain ||
		storage.Provider != config.CredentialProviderMacOS ||
		storage.Service != config.DefaultMacOSKeychainTokenService ||
		storage.Account != "api:https://api.example.com" {
		t.Fatalf("keychain storage = %#v", storage)
	}
}

func TestWaitForLegacyDeviceLoginTokenTerminalStatuses(t *testing.T) {
	cases := []struct {
		name    string
		status  string
		token   string
		wantErr string
	}{
		{name: "issued", status: "issued", token: "login-token"},
		{name: "issued empty token", status: "issued", wantErr: "login token is empty"},
		{name: "denied", status: "denied", wantErr: "login request was denied"},
		{name: "expired", status: "expired", wantErr: "login expired"},
		{name: "consumed", status: "consumed", wantErr: "already used"},
		{name: "unexpected", status: "weird", wantErr: "unexpected login status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := legacyLoginClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != "/api/rstream/login/requests/request%2Fid/token" {
					http.NotFound(w, r)
					return
				}
				var req controlplane.RstreamLoginTokenRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if req.RequestSecret != "secret" {
					t.Fatalf("request secret = %q", req.RequestSecret)
				}
				_ = json.NewEncoder(w).Encode(controlplane.RstreamLoginTokenResponse{Status: tc.status, Token: tc.token})
			})
			token, err := waitForLegacyDeviceLoginToken(t.Context(), client, controlplane.RstreamLoginResponse{RequestID: "request/id", RequestSecret: "secret"})
			if tc.wantErr == "" {
				if err != nil || token != tc.token {
					t.Fatalf("wait result token=%q err=%v", token, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %q error, got token=%q err=%v", tc.wantErr, token, err)
			}
		})
	}
}

func TestWaitForLegacyDeviceLoginTokenRejectsAlreadyExpiredResponse(t *testing.T) {
	expired := time.Now().Add(-time.Second)
	token, err := waitForLegacyDeviceLoginToken(t.Context(), controlplane.NewClient("http://127.0.0.1:1", ""), controlplane.RstreamLoginResponse{ExpiresAt: &expired})
	if err == nil || !strings.Contains(err.Error(), "login expired") || token != "" {
		t.Fatalf("expired result token=%q err=%v", token, err)
	}
}

func TestWaitForOAuthDeviceTokenTerminalResponses(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    map[string]string
		want    string
		wantErr string
	}{
		{name: "success", status: http.StatusOK, body: map[string]string{"access_token": "oauth-token"}, want: "oauth-token"},
		{name: "success empty token", status: http.StatusOK, body: map[string]string{"access_token": ""}, wantErr: "login token is empty"},
		{name: "denied", status: http.StatusBadRequest, body: map[string]string{"error": "access_denied"}, wantErr: "login request was denied"},
		{name: "expired", status: http.StatusBadRequest, body: map[string]string{"error": "expired_token"}, wantErr: "login expired"},
		{name: "invalid grant", status: http.StatusBadRequest, body: map[string]string{"error": "invalid_grant"}, wantErr: "device code is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/oauth/token" {
					http.NotFound(w, r)
					return
				}
				if err := r.ParseForm(); err != nil {
					t.Fatalf("ParseForm() error = %v", err)
				}
				if r.Form.Get("device_code") != "device" || r.Form.Get("client_id") != rstreamOAuthClientID {
					t.Fatalf("unexpected form: %#v", r.Form)
				}
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer server.Close()
			token, err := waitForOAuthDeviceToken(t.Context(), controlplane.NewClient(server.URL, ""), "/oauth/token", controlplane.OAuthDeviceAuthorizationResponse{DeviceCode: "device", ExpiresIn: 60})
			if tc.wantErr == "" {
				if err != nil || token != tc.want {
					t.Fatalf("wait result token=%q err=%v", token, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected %q error, got token=%q err=%v", tc.wantErr, token, err)
			}
		})
	}
}

func TestOAuthPollErrorMappingAndIntervals(t *testing.T) {
	interval := 2 * time.Second
	next, pending, err := resolveOAuthPollError(t.Context(), &controlplane.OAuthError{Code: "authorization_pending"}, interval)
	if err != nil || !pending || next != interval {
		t.Fatalf("authorization_pending = %s %v %v", next, pending, err)
	}
	next, pending, err = resolveOAuthPollError(t.Context(), &controlplane.OAuthError{Code: "slow_down"}, interval)
	if err != nil || !pending || next != 7*time.Second {
		t.Fatalf("slow_down = %s %v %v", next, pending, err)
	}
	_, pending, err = resolveOAuthPollError(t.Context(), errors.New("network"), interval)
	if err == nil || pending {
		t.Fatalf("non OAuth error = pending %v err %v", pending, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, pending, err = resolveOAuthPollError(ctx, &controlplane.OAuthError{Code: "authorization_pending"}, interval)
	if err == nil || pending {
		t.Fatalf("canceled poll = pending %v err %v", pending, err)
	}
	if oauthDeviceIntervalSeconds(0) != 5 || oauthDeviceIntervalSeconds(3) != 3 {
		t.Fatalf("unexpected oauth interval defaults")
	}
	if err := rstreamLoginPollError(context.DeadlineExceeded); err == nil || !strings.Contains(err.Error(), "login expired") {
		t.Fatalf("deadline poll error = %v", err)
	}
	if err := rstreamLoginCommandError(context.Canceled); err == nil || !strings.Contains(err.Error(), "login canceled") {
		t.Fatalf("command canceled error = %v", err)
	}
}

func TestWriteLoginAndStructuredOutput(t *testing.T) {
	command := loginOutputCommand("json")
	out, err := captureStdout(t, func() error {
		return writeLoginResult(command, loginResult{Authenticated: true, APIURL: "https://api.example.com", AuthFlow: loginAuthFlowOAuth})
	})
	if err != nil || !strings.Contains(out, `"authenticated": true`) || !strings.Contains(out, `"authFlow": "oauth"`) {
		t.Fatalf("writeLoginResult(json) out=%q err=%v", out, err)
	}
	command = loginOutputCommand("xml")
	if err := writeLoginResult(command, loginResult{}); err == nil || !strings.Contains(err.Error(), "invalid --output") {
		t.Fatalf("expected invalid output error, got %v", err)
	}
	out, err = captureStdout(t, func() error {
		return writeStructuredOutput("yaml", map[string]string{"status": "ok"})
	})
	if err != nil || !strings.Contains(out, "status: ok") {
		t.Fatalf("writeStructuredOutput(yaml) out=%q err=%v", out, err)
	}
	if err := writeStructuredOutput("xml", map[string]string{}); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("expected unsupported output error, got %v", err)
	}
}

func legacyLoginClient(t *testing.T, h http.HandlerFunc) *controlplane.Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	return controlplane.NewClient(server.URL, "")
}

func loginOutputCommand(output string) *cobra.Command {
	command := &cobra.Command{Use: "login-test"}
	command.Flags().String("output", output, "")
	return command
}

func loginTokenStorageCommand() *cobra.Command {
	command := &cobra.Command{Use: "login-token-storage-test"}
	command.Flags().String("token-storage", tokenStorageInline, "")
	return command
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() error = %v", err)
	}
	os.Stdout = write
	runErr := fn()
	_ = write.Close()
	os.Stdout = old
	out, readErr := io.ReadAll(read)
	if readErr != nil {
		t.Fatalf("ReadAll() error = %v", readErr)
	}
	return string(out), runErr
}
