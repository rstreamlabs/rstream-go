// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

func newRuntimeControlPlaneClient(resolved config.Resolved, opts ...controlplane.Option) *controlplane.Client {
	opts = append(opts, controlplane.WithHeaders(resolved.ControlPlaneHeaders))
	return controlplane.NewClient(resolved.APIURL, resolved.Token, opts...)
}

func validateToken(ctx context.Context, apiURL, token string, headers map[string]string) error {
	_, err := validateTokenIdentity(ctx, apiURL, token, headers)
	return err
}

func validateTokenIdentity(ctx context.Context, apiURL, token string, headers map[string]string) (controlplane.CallerIdentity, error) {
	logger := slog.With("component", "control_plane_api.validate-token")
	if strings.TrimSpace(token) == "" {
		return controlplane.CallerIdentity{}, errors.New("token is required")
	}
	client := controlplane.NewClient(apiURL, token, controlplane.WithHeaders(headers), controlplane.WithLogger(logger))
	identity, err := client.Identity(ctx)
	if err != nil {
		if errors.Is(err, controlplane.ErrUnauthorized) {
			return controlplane.CallerIdentity{}, errors.New("token validation failed: not authenticated")
		}
		return controlplane.CallerIdentity{}, err
	}
	if flagVerbose {
		logger.Debug("token validated", "owner_kind", identity.Owner.Kind, "user_id", identity.Owner.UserID, "workspace_id", identity.Owner.WorkspaceID, "credential_id", identity.CredentialID)
	}
	return identity, nil
}

func resolveControlPlaneToken(cfg config.Config, apiURL string) (string, error) {
	if token := config.ReadEnv().Token; token != "" {
		return token, nil
	}
	env, _ := cfg.FindEnvironment(apiURL)
	if env == nil {
		return "", nil
	}
	token, ok, err := config.TokenFromAuth(env.Auth)
	if err != nil {
		return "", err
	}
	if ok {
		return token, nil
	}
	return "", nil
}
