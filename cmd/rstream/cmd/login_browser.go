// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

// Compatibility request for servers without owner-aware enrollment. Current
// servers define their default in the shared versioned enrollment catalog.
var rstreamLoginPermissions = []string{
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

const rstreamLoginPollInterval = 2 * time.Second
const rstreamLoginTimeout = 10 * time.Minute
const rstreamOAuthClientID = "rstream-cli"

const (
	tokenStorageInline        = "inline"
	tokenStorageMacOSKeychain = "macos-keychain"
)

var storeTokenInCredentialProvider = config.StoreToken

func storeToken(ctx context.Context, path string, cfg config.Config, apiURL, token string) error {
	return storeTokenWithStorage(ctx, path, cfg, apiURL, token, config.TokenStorage{Kind: config.TokenStorageInline, Value: token})
}

func storeTokenFromFlags(cmd *cobra.Command, path string, cfg config.Config, apiURL, token string) (controlplane.CallerIdentity, error) {
	storage, err := tokenStorageFromFlags(cmd, apiURL)
	if err != nil {
		return controlplane.CallerIdentity{}, err
	}
	return storeTokenAndIdentity(cmd.Context(), path, cfg, apiURL, token, storage)
}

func storeTokenWithStorage(ctx context.Context, path string, cfg config.Config, apiURL, token string, storage config.TokenStorage) error {
	_, err := storeTokenAndIdentity(ctx, path, cfg, apiURL, token, storage)
	return err
}

func storeTokenAndIdentity(ctx context.Context, path string, cfg config.Config, apiURL, token string, storage config.TokenStorage) (controlplane.CallerIdentity, error) {
	env, _ := cfg.FindEnvironment(apiURL)
	headers, err := config.ResolveControlPlaneHeaders(env, config.ReadEnv().ControlPlaneHeaders)
	if err != nil {
		return controlplane.CallerIdentity{}, err
	}
	identity, err := validateTokenIdentity(ctx, apiURL, token, headers)
	if err != nil {
		return controlplane.CallerIdentity{}, err
	}
	switch storage.Kind {
	case config.TokenStorageInline:
	case config.TokenStorageKeychain:
		storage = loginIdentityTokenStorage(storage, identity, token)
		if err := storeTokenInCredentialProvider(storage, token); err != nil {
			return controlplane.CallerIdentity{}, err
		}
	default:
		return controlplane.CallerIdentity{}, fmt.Errorf("token storage kind %q is not supported", storage.Kind)
	}
	err = config.UpdateAtomic(path, func(latest *config.Config) error {
		environment := latest.EnsureEnvironment(apiURL)
		if storage.Kind == config.TokenStorageInline {
			return setEnvironmentToken(environment, token)
		}
		setEnvironmentTokenStorage(environment, storage)
		return nil
	})
	if err != nil {
		return controlplane.CallerIdentity{}, err
	}
	return identity, nil
}

// Each approved proof gets its own slot. Switching owner or re-enrolling must
// not change a token still referenced by an existing context or process.
func loginIdentityTokenStorage(storage config.TokenStorage, identity controlplane.CallerIdentity, token string) config.TokenStorage {
	ownerID := identity.Owner.UserID
	if identity.Owner.Kind == "workspace" {
		ownerID = identity.Owner.WorkspaceID
	}
	storage.Account += fmt.Sprintf(":owner:%s:%s:token:%x", identity.Owner.Kind, url.QueryEscape(ownerID), sha256.Sum256([]byte(token)))
	return storage
}

func tokenStorageFromFlags(cmd *cobra.Command, apiURL string) (config.TokenStorage, error) {
	storage, _ := cmd.Flags().GetString("token-storage")
	switch storage {
	case "", tokenStorageInline:
		return config.TokenStorage{Kind: config.TokenStorageInline}, nil
	case tokenStorageMacOSKeychain:
		return config.NewMacOSKeychainTokenStorage(apiURL), nil
	default:
		return config.TokenStorage{}, fmt.Errorf("invalid --token-storage %q (valid: inline, macos-keychain)", storage)
	}
}

func runLegacyDeviceLogin(cmd *cobra.Command, path string, cfg config.Config, apiURL string) error {
	ctx := cmd.Context()
	env, _ := cfg.FindEnvironment(apiURL)
	headers, err := config.ResolveControlPlaneHeaders(env, config.ReadEnv().ControlPlaneHeaders)
	if err != nil {
		return err
	}
	client := controlplane.NewClient(apiURL, "", controlplane.WithHeaders(headers))
	req := controlplane.RstreamLoginRequest{Permissions: rstreamLoginPermissions, Source: resolveRstreamLoginSource()}
	metadata, err := client.OAuthAuthorizationServerMetadata(ctx)
	if err != nil {
		var apiError *controlplane.APIError
		if !errors.As(err, &apiError) || (apiError.StatusCode != http.StatusNotFound && apiError.StatusCode != http.StatusMethodNotAllowed) {
			return rstreamLoginCommandError(err)
		}
	} else if metadata.SupportsCredentialOwnerVersion(controlplane.NativeCredentialOwnerVersion) {
		req.CredentialOwnerVersion = controlplane.NativeCredentialOwnerVersion
		req.Permissions = nil
	}
	res, err := client.CreateRstreamLogin(ctx, req)
	if err != nil {
		return rstreamLoginCommandError(err)
	}
	if res.RequestID == "" || res.RequestSecret == "" || res.URL == "" {
		return errors.New("rstream login response is invalid")
	}
	progress := loginProgressOutput(cmd)
	fmt.Fprintln(progress, "Open this URL in your browser to continue login:")
	fmt.Fprintln(progress, res.URL)
	if err := openBrowser(res.URL); err != nil {
		fmt.Fprintln(os.Stderr, "Unable to open the browser automatically.")
	}
	fmt.Fprintln(progress, "Waiting for approval...")
	token, err := waitForLegacyDeviceLoginToken(ctx, client, res)
	if err != nil {
		return rstreamLoginCommandError(err)
	}
	return rstreamLoginCommandError(completeLogin(cmd, path, cfg, apiURL, token, loginAuthFlowLegacy))
}

func runOAuthDeviceLogin(cmd *cobra.Command, path string, cfg config.Config, apiURL string) error {
	ctx := cmd.Context()
	env, _ := cfg.FindEnvironment(apiURL)
	headers, err := config.ResolveControlPlaneHeaders(env, config.ReadEnv().ControlPlaneHeaders)
	if err != nil {
		return err
	}
	client := controlplane.NewClient(apiURL, "", controlplane.WithHeaders(headers))
	metadata, err := client.OAuthAuthorizationServerMetadata(ctx)
	if err != nil {
		return rstreamLoginCommandError(err)
	}
	if metadata.DeviceAuthorizationEndpoint == "" || metadata.TokenEndpoint == "" {
		return errors.New("OAuth device authorization metadata is incomplete")
	}
	req, err := nativeOAuthDeviceLoginRequest(metadata, nil)
	if err != nil {
		return err
	}
	res, err := client.CreateOAuthDeviceAuthorization(ctx, metadata.DeviceAuthorizationEndpoint, req)
	if err != nil {
		return rstreamLoginCommandError(err)
	}
	if res.DeviceCode == "" || res.UserCode == "" || res.VerificationURI == "" {
		return errors.New("OAuth device authorization response is invalid")
	}
	loginURL := res.VerificationURIComplete
	if loginURL == "" {
		loginURL = res.VerificationURI
	}
	progress := loginProgressOutput(cmd)
	fmt.Fprintln(progress, "Open this URL in your browser to continue login:")
	fmt.Fprintln(progress, loginURL)
	if res.VerificationURIComplete == "" {
		fmt.Fprintf(progress, "User code: %s\n", res.UserCode)
	}
	if err := openBrowser(loginURL); err != nil {
		fmt.Fprintln(os.Stderr, "Unable to open the browser automatically.")
	}
	fmt.Fprintln(progress, "Waiting for approval...")
	token, err := waitForOAuthDeviceToken(ctx, client, metadata.TokenEndpoint, res)
	if err != nil {
		return rstreamLoginCommandError(err)
	}
	return rstreamLoginCommandError(completeLogin(cmd, path, cfg, apiURL, token, loginAuthFlowOAuth))
}

// Nil permissions selects the server's default on capable servers. Explicit
// scopes (for example an MCP request) remain an independent maximum.
func nativeOAuthDeviceLoginRequest(metadata controlplane.OAuthAuthorizationServerMetadata, permissions []string) (controlplane.OAuthDeviceAuthorizationRequest, error) {
	if permissions != nil && len(permissions) == 0 {
		return controlplane.OAuthDeviceAuthorizationRequest{}, errors.New("at least one permission must be requested")
	}
	version := 0
	if metadata.SupportsCredentialOwnerVersion(controlplane.NativeCredentialOwnerVersion) {
		version = controlplane.NativeCredentialOwnerVersion
	} else if permissions == nil {
		permissions = rstreamLoginPermissions
	}
	return controlplane.OAuthDeviceAuthorizationRequest{ClientID: rstreamOAuthClientID, Scope: strings.Join(permissions, " "), Source: resolveRstreamLoginSource(), CredentialOwnerVersion: version}, nil
}

func loginProgressOutput(cmd *cobra.Command) *os.File {
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return os.Stderr
	}
	return os.Stdout
}

func waitForLegacyDeviceLoginToken(ctx context.Context, client *controlplane.Client, res controlplane.RstreamLoginResponse) (string, error) {
	deadline := time.Now().Add(rstreamLoginTimeout)
	if res.ExpiresAt != nil && !res.ExpiresAt.IsZero() {
		deadline = *res.ExpiresAt
	}
	if time.Now().After(deadline) {
		return "", errors.New("login expired")
	}
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ticker := time.NewTicker(rstreamLoginPollInterval)
	defer ticker.Stop()
	for {
		resp, err := client.ExchangeRstreamLoginToken(pollCtx, res.RequestID, controlplane.RstreamLoginTokenRequest{RequestSecret: res.RequestSecret})
		if err != nil {
			if pollCtx.Err() != nil {
				return "", rstreamLoginPollError(pollCtx.Err())
			}
			return "", err
		}
		switch resp.Status {
		case "issued":
			if resp.Token == "" {
				return "", errors.New("login token is empty")
			}
			return resp.Token, nil
		case "pending":
			select {
			case <-pollCtx.Done():
				return "", rstreamLoginPollError(pollCtx.Err())
			case <-ticker.C:
				continue
			}
		case "denied":
			return "", errors.New("login request was denied")
		case "expired":
			return "", errors.New("login expired")
		case "consumed":
			return "", errors.New("login token was already used")
		default:
			return "", fmt.Errorf("unexpected login status: %s", resp.Status)
		}
	}
}

func waitForOAuthDeviceToken(ctx context.Context, client *controlplane.Client, tokenEndpoint string, res controlplane.OAuthDeviceAuthorizationResponse) (string, error) {
	deadline := time.Now().Add(rstreamLoginTimeout)
	if res.ExpiresIn > 0 {
		deadline = time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)
	}
	if time.Now().After(deadline) {
		return "", errors.New("login expired")
	}
	pollCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	interval := time.Duration(oauthDeviceIntervalSeconds(res.Interval)) * time.Second
	for {
		resp, err := client.ExchangeOAuthDeviceToken(pollCtx, tokenEndpoint, controlplane.OAuthDeviceTokenRequest{ClientID: rstreamOAuthClientID, DeviceCode: res.DeviceCode})
		if err == nil {
			if resp.AccessToken == "" {
				return "", errors.New("login token is empty")
			}
			return resp.AccessToken, nil
		}
		nextInterval, pending, pollErr := resolveOAuthPollError(pollCtx, err, interval)
		if pollErr != nil {
			return "", pollErr
		}
		if pending {
			interval = nextInterval
			if err := waitOAuthPollInterval(pollCtx, interval); err != nil {
				return "", rstreamLoginPollError(err)
			}
			continue
		}
		return "", err
	}
}

func resolveOAuthPollError(ctx context.Context, err error, interval time.Duration) (time.Duration, bool, error) {
	if ctx.Err() != nil {
		return interval, false, rstreamLoginPollError(ctx.Err())
	}
	var oauthErr *controlplane.OAuthError
	if !errors.As(err, &oauthErr) {
		return interval, false, err
	}
	switch oauthErr.Code {
	case "authorization_pending":
		return interval, true, nil
	case "slow_down":
		return interval + 5*time.Second, true, nil
	case "access_denied":
		return interval, false, errors.New("login request was denied")
	case "expired_token":
		return interval, false, errors.New("login expired")
	case "invalid_grant":
		return interval, false, errors.New("login device code is invalid")
	default:
		return interval, false, oauthErr
	}
}

func waitOAuthPollInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func oauthDeviceIntervalSeconds(value int) int {
	if value > 0 {
		return value
	}
	return 5
}

func rstreamLoginPollError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("login expired")
	}
	return err
}

func rstreamLoginCommandError(err error) error {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr)
		return errors.New("login canceled")
	}
	return err
}

func resolveRstreamLoginSource() []controlplane.RstreamLabel {
	labels := make([]controlplane.RstreamLabel, 0, 6)
	if rstream.Agent != "" {
		labels = append(labels, controlplane.RstreamLabel{Key: "agent", Value: rstream.Agent, Label: "Agent"})
	}
	if rstream.Channel != "" {
		labels = append(labels, controlplane.RstreamLabel{Key: "channel", Value: rstream.Channel, Label: "Channel"})
	}
	if rstream.Version != "" {
		labels = append(labels, controlplane.RstreamLabel{Key: "version", Value: rstream.Version, Label: "CLI version"})
	}
	identity := rstream.CompiletimeIdentity()
	if identity.OS == "" || identity.Arch == "" {
		runtimeIdentity := rstream.RuntimeIdentity()
		if identity.OS == "" {
			identity.OS = runtimeIdentity.OS
		}
		if identity.Arch == "" {
			identity.Arch = runtimeIdentity.Arch
		}
	}
	if identity.OS != "" {
		labels = append(labels, controlplane.RstreamLabel{Key: "os", Value: identity.OS, Label: "OS"})
	}
	if identity.Arch != "" {
		labels = append(labels, controlplane.RstreamLabel{Key: "arch", Value: identity.Arch, Label: "Arch"})
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		labels = append(labels, controlplane.RstreamLabel{Key: "hostname", Value: hostname, Label: "Hostname"})
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}
