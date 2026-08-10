// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

type mcpAuthSession struct {
	ID                      string    `json:"id"`
	APIURL                  string    `json:"api_url"`
	ConfigPath              string    `json:"config_path"`
	DeviceCode              string    `json:"device_code"`
	Scopes                  []string  `json:"scopes,omitempty"`
	TokenEndpoint           string    `json:"token_endpoint"`
	UserCode                string    `json:"user_code"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete,omitempty"`
	ExpiresAt               time.Time `json:"expires_at"`
	IntervalSeconds         int       `json:"interval_seconds"`
	CreatedAt               time.Time `json:"created_at"`
}

type mcpAuthRegistryFile struct {
	Sessions []mcpAuthSession `json:"sessions"`
}

var rstreamMCPLoginPermissions = []string{
	"account.plan.read-only",
	"account.projects.read-write",
	"account.tokens.create",
	"account.workspaces.read-only",
	"account.workspace-protection.read-write",
	"network.streams.read-only",
	"network.events.read-only",
	"network.webhooks.read-only",
	"network.webtty-servers.read-only",
	"tunnels.resources.read-only",
	"tunnels.streams.create-delete",
	"tunnels.tunnels.create-delete",
	"turn.credentials.create",
	"turn.relay.allocate",
}

func mcpAuthStart(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	payload, err := mcpCreateAuthSession(ctx, args)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(payload, false)
}

func mcpCreateAuthSession(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	configPath, cfg, err := mcpLoadConfig()
	if err != nil {
		return nil, err
	}
	apiURL, err := mcpAuthAPIURL(args, cfg)
	if err != nil {
		return nil, err
	}
	permissions, err := mcpAuthPermissions(args)
	if err != nil {
		return nil, err
	}
	environment, _ := cfg.FindEnvironment(apiURL)
	headers, err := config.ResolveControlPlaneHeaders(environment, config.ReadEnv().ControlPlaneHeaders)
	if err != nil {
		return nil, err
	}
	client := controlplane.NewClient(apiURL, "", controlplane.WithHeaders(headers))
	metadata, err := client.OAuthAuthorizationServerMetadata(ctx)
	if err != nil {
		return nil, rstreamLoginCommandError(err)
	}
	if metadata.DeviceAuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
		return nil, errors.New("OAuth device authorization metadata is incomplete")
	}
	response, err := client.CreateOAuthDeviceAuthorization(ctx, metadata.DeviceAuthorizationEndpoint, controlplane.OAuthDeviceAuthorizationRequest{ClientID: rstreamOAuthClientID, Scope: strings.Join(permissions, " "), Source: resolveRstreamLoginSource()})
	if err != nil {
		return nil, rstreamLoginCommandError(err)
	}
	if response.DeviceCode == "" || response.UserCode == "" || response.VerificationURI == "" {
		return nil, errors.New("OAuth device authorization response is invalid")
	}
	session := mcpAuthSession{ID: mcpAuthID(), APIURL: apiURL, ConfigPath: configPath, DeviceCode: response.DeviceCode, Scopes: permissions, TokenEndpoint: metadata.TokenEndpoint, UserCode: response.UserCode, VerificationURI: response.VerificationURI, VerificationURIComplete: response.VerificationURIComplete, ExpiresAt: time.Now().UTC().Add(time.Duration(response.ExpiresIn) * time.Second), IntervalSeconds: oauthDeviceIntervalSeconds(response.Interval), CreatedAt: time.Now().UTC()}
	if response.ExpiresIn <= 0 {
		session.ExpiresAt = time.Now().UTC().Add(rstreamLoginTimeout)
	}
	if err := mcpAuthAddSession(mcpAuthRegistryPath(configPath), session); err != nil {
		return nil, err
	}
	return mcpAuthSessionResponse(session, cfg), nil
}

func mcpAuthPoll(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	id, err := mcpRequiredStringArg(args, "id")
	if err != nil {
		return nil, err
	}
	wait, err := mcpOptionalBoolArg(args, "wait", false)
	if err != nil {
		return nil, err
	}
	timeoutSeconds, err := mcpOptionalIntArg(args, "timeout_seconds")
	if err != nil {
		return nil, err
	}
	session, err := mcpAuthReadSessionFromCurrentConfig(id)
	if err != nil {
		return nil, err
	}
	if wait {
		return mcpAuthPollWait(ctx, session, timeoutSeconds)
	}
	return mcpAuthPollOnce(ctx, session)
}

func mcpAuthPollWait(ctx context.Context, session mcpAuthSession, timeoutSeconds *int) (map[string]any, error) {
	timeout := 120 * time.Second
	if timeoutSeconds != nil && *timeoutSeconds > 0 {
		timeout = time.Duration(*timeoutSeconds) * time.Second
	}
	if timeout > 300*time.Second {
		timeout = 300 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return mcpJSONResult(map[string]any{"authenticated": false, "status": "pending", "id": session.ID, "next_poll_after_seconds": session.IntervalSeconds, "expires_at": session.ExpiresAt}, false)
		}
		result, done, err := mcpAuthExchangeRaw(ctx, session)
		if err != nil || done {
			if result == nil {
				return nil, err
			}
			return mcpJSONResult(result, false)
		}
		interval := time.Duration(session.IntervalSeconds) * time.Second
		if next, ok := result["next_poll_after_seconds"].(int); ok && next > 0 {
			session.IntervalSeconds = next
			interval = time.Duration(next) * time.Second
		}
		if remaining := time.Until(deadline); remaining < interval {
			interval = remaining
		}
		if interval <= 0 {
			return mcpJSONResult(map[string]any{"authenticated": false, "status": "pending", "id": session.ID, "next_poll_after_seconds": session.IntervalSeconds, "expires_at": session.ExpiresAt}, false)
		}
		if err := waitOAuthPollInterval(ctx, interval); err != nil {
			return nil, err
		}
	}
}

func mcpAuthPollOnce(ctx context.Context, session mcpAuthSession) (map[string]any, error) {
	result, _, err := mcpAuthExchangeRaw(ctx, session)
	if err != nil {
		return nil, err
	}
	return mcpJSONResult(result, false)
}

func mcpAuthExchangeRaw(ctx context.Context, session mcpAuthSession) (map[string]any, bool, error) {
	if time.Now().UTC().After(session.ExpiresAt) {
		_ = mcpAuthRemoveSession(mcpAuthRegistryPath(session.ConfigPath), session.ID)
		return nil, true, errors.New("login expired")
	}
	_, cfg, err := mcpLoadConfig()
	if err != nil {
		return nil, true, err
	}
	environment, _ := cfg.FindEnvironment(session.APIURL)
	headers, err := config.ResolveControlPlaneHeaders(environment, config.ReadEnv().ControlPlaneHeaders)
	if err != nil {
		return nil, true, err
	}
	client := controlplane.NewClient(session.APIURL, "", controlplane.WithHeaders(headers))
	response, err := client.ExchangeOAuthDeviceToken(ctx, session.TokenEndpoint, controlplane.OAuthDeviceTokenRequest{ClientID: rstreamOAuthClientID, DeviceCode: session.DeviceCode})
	if err == nil {
		if response.AccessToken == "" {
			return nil, true, errors.New("login token is empty")
		}
		if err := storeToken(ctx, session.ConfigPath, cfg, session.APIURL, response.AccessToken); err != nil {
			return nil, true, err
		}
		_ = mcpAuthRemoveSession(mcpAuthRegistryPath(session.ConfigPath), session.ID)
		return map[string]any{"authenticated": true, "status": "authenticated", "api_url": session.APIURL, "config_path": session.ConfigPath, "auth_flow": loginAuthFlowOAuth}, true, nil
	}
	result, pending, pollErr := mcpAuthPollResult(ctx, session, err)
	if pollErr != nil {
		_ = mcpAuthRemoveSession(mcpAuthRegistryPath(session.ConfigPath), session.ID)
		return nil, true, pollErr
	}
	if pending {
		return result, false, nil
	}
	return nil, true, err
}

func mcpAuthPollResult(ctx context.Context, session mcpAuthSession, err error) (map[string]any, bool, error) {
	if ctx.Err() != nil {
		return nil, false, rstreamLoginPollError(ctx.Err())
	}
	var oauthErr *controlplane.OAuthError
	if !errors.As(err, &oauthErr) {
		return nil, false, err
	}
	switch oauthErr.Code {
	case "authorization_pending":
		return map[string]any{"authenticated": false, "status": "pending", "id": session.ID, "next_poll_after_seconds": session.IntervalSeconds, "expires_at": session.ExpiresAt}, true, nil
	case "slow_down":
		session.IntervalSeconds += 5
		if updateErr := mcpAuthReplaceSession(mcpAuthRegistryPath(session.ConfigPath), session); updateErr != nil {
			return nil, false, updateErr
		}
		return map[string]any{"authenticated": false, "status": "pending", "id": session.ID, "next_poll_after_seconds": session.IntervalSeconds, "expires_at": session.ExpiresAt}, true, nil
	case "access_denied":
		return nil, false, errors.New("login request was denied")
	case "expired_token":
		return nil, false, errors.New("login expired")
	case "invalid_grant":
		return nil, false, errors.New("login device code is invalid")
	default:
		return nil, false, oauthErr
	}
}

func mcpAuthAPIURL(args map[string]json.RawMessage, cfg config.Config) (string, error) {
	apiURL, err := mcpOptionalStringArg(args, "api_url", "")
	if err != nil {
		return "", err
	}
	if apiURL != "" {
		return config.NormalizeAPIURL(apiURL), nil
	}
	if envURL := config.ReadEnv().APIURL; envURL != "" {
		return envURL, nil
	}
	if contextName := mcpSelectedContextName(cfg); contextName != "" {
		contextValue, _, err := cfg.FindContextByName(contextName)
		if err != nil {
			return "", err
		}
		if contextValue != nil && strings.TrimSpace(contextValue.APIURL) != "" {
			return config.NormalizeAPIURL(contextValue.APIURL), nil
		}
	}
	if len(cfg.Environments) == 1 && strings.TrimSpace(cfg.Environments[0].APIURL) != "" {
		return config.NormalizeAPIURL(cfg.Environments[0].APIURL), nil
	}
	return config.DefaultAPIURL(), nil
}

func mcpAuthPermissions(args map[string]json.RawMessage) ([]string, error) {
	permissions, err := mcpOptionalStringSliceArg(args, "permissions")
	if err != nil {
		return nil, err
	}
	if len(permissions) == 0 {
		return append([]string(nil), rstreamMCPLoginPermissions...), nil
	}
	return mcpUnionPermissions(rstreamMCPLoginPermissions, permissions), nil
}

func mcpUnionPermissions(base []string, extra []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(extra))
	for _, permission := range append(append([]string(nil), base...), extra...) {
		permission = strings.TrimSpace(permission)
		if permission == "" || seen[permission] {
			continue
		}
		if permission == "account.projects.read-only" && seen["account.projects.read-write"] {
			continue
		}
		if permission == "account.projects.read-write" && seen["account.projects.read-only"] {
			out = removeString(out, "account.projects.read-only")
			delete(seen, "account.projects.read-only")
		}
		seen[permission] = true
		out = append(out, permission)
	}
	return out
}

func removeString(values []string, remove string) []string {
	out := values[:0]
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}

func mcpAuthSessionResponse(session mcpAuthSession, cfg config.Config) map[string]any {
	loginURL := session.VerificationURIComplete
	if loginURL == "" {
		loginURL = session.VerificationURI
	}
	defaultContext := ""
	if cfg.Defaults.Context != nil {
		defaultContext = cfg.Defaults.Context.Name
	}
	scopes := session.Scopes
	if len(scopes) == 0 {
		scopes = rstreamMCPLoginPermissions
	}
	response := map[string]any{"id": session.ID, "api_url": session.APIURL, "config_path": session.ConfigPath, "default_context": defaultContext, "verification_uri": session.VerificationURI, "verification_uri_complete": session.VerificationURIComplete, "login_url": loginURL, "expires_at": session.ExpiresAt, "interval_seconds": session.IntervalSeconds, "scopes": scopes}
	if session.VerificationURIComplete == "" {
		response["user_code"] = session.UserCode
	}
	return response
}

func mcpAuthReadSessionFromCurrentConfig(id string) (mcpAuthSession, error) {
	configPath, _, err := mcpLoadConfig()
	if err != nil {
		return mcpAuthSession{}, err
	}
	sessions, err := readMCPAuthRegistry(mcpAuthRegistryPath(configPath))
	if err != nil {
		return mcpAuthSession{}, err
	}
	for _, session := range sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return mcpAuthSession{}, fmt.Errorf("auth session %q not found", id)
}

func mcpAuthRegistryPath(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		path, err := config.DefaultConfigPath()
		if err == nil {
			configPath = path
		}
	}
	return filepath.Join(filepath.Dir(configPath), "mcp-auth.json")
}

func mcpAuthAddSession(path string, session mcpAuthSession) error {
	return updateMCPAuthRegistry(path, func(sessions []mcpAuthSession) ([]mcpAuthSession, error) {
		next := make([]mcpAuthSession, 0, len(sessions)+1)
		now := time.Now().UTC()
		for _, existing := range sessions {
			if existing.ID != session.ID && now.Before(existing.ExpiresAt) {
				next = append(next, existing)
			}
		}
		next = append(next, session)
		return next, nil
	})
}

func mcpAuthReplaceSession(path string, session mcpAuthSession) error {
	return updateMCPAuthRegistry(path, func(sessions []mcpAuthSession) ([]mcpAuthSession, error) {
		next := make([]mcpAuthSession, 0, len(sessions))
		for _, existing := range sessions {
			if existing.ID == session.ID {
				next = append(next, session)
			} else {
				next = append(next, existing)
			}
		}
		return next, nil
	})
}

func mcpAuthRemoveSession(path string, id string) error {
	return updateMCPAuthRegistry(path, func(sessions []mcpAuthSession) ([]mcpAuthSession, error) {
		next := make([]mcpAuthSession, 0, len(sessions))
		for _, session := range sessions {
			if session.ID != id {
				next = append(next, session)
			}
		}
		return next, nil
	})
}

func readMCPAuthRegistry(path string) ([]mcpAuthSession, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []mcpAuthSession{}, nil
		}
		return nil, err
	}
	defer file.Close()
	var data mcpAuthRegistryFile
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, err
	}
	return data.Sessions, nil
}

func updateMCPAuthRegistry(path string, update func([]mcpAuthSession) ([]mcpAuthSession, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := config.LockFile(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Unlock()
	sessions, err := readMCPAuthRegistry(path)
	if err != nil {
		return err
	}
	next, err := update(sessions)
	if err != nil {
		return err
	}
	return writeMCPAuthRegistry(path, next)
}

func writeMCPAuthRegistry(path string, sessions []mcpAuthSession) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-auth-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(mcpAuthRegistryFile{Sessions: sessions}); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func mcpAuthID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("auth-%d", time.Now().UnixNano())
	}
	return "auth-" + hex.EncodeToString(buf[:])
}
