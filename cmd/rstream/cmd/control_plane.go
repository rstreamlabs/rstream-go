// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/controlplane"
	"github.com/rstreamlabs/rstream-go/config"
)

func validateToken(ctx context.Context, apiURL, token string) error {
	logger := slog.With("component", "control-plane.validate-token")
	if strings.TrimSpace(token) == "" {
		return errors.New("token is required")
	}
	client := controlplane.NewClient(apiURL, token, controlplane.WithLogger(logger))
	whoami, err := client.Whoami(ctx)
	if err != nil {
		if errors.Is(err, controlplane.ErrUnauthorized) {
			return errors.New("token validation failed: not authenticated")
		}
		return err
	}
	if flagVerbose {
		logger.Debug("token validated", "id", whoami.ID, "role", whoami.Role)
	}
	return nil
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
