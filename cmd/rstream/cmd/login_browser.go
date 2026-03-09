// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/controlplane"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/spf13/cobra"
)

var rstreamLoginPermissions = []string{
	"account.projects.read-only",
	"network.tunnels.create-delete",
	"network.streams.create-delete",
	"network.resources.read-only",
}

const rstreamLoginPollInterval = 2 * time.Second
const rstreamLoginTimeout = 10 * time.Minute

func storeToken(ctx context.Context, path string, cfg config.Config, apiURL, token string) error {
	if err := validateToken(ctx, apiURL, token); err != nil {
		return err
	}
	env := cfg.EnsureEnvironment(apiURL)
	if err := setEnvironmentToken(env, token); err != nil {
		return err
	}
	if err := config.WriteAtomic(path, cfg); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Login successful.")
	return nil
}

func runRstreamLogin(cmd *cobra.Command, path string, cfg config.Config, apiURL string) error {
	ctx := cmd.Context()
	client := controlplane.NewClient(apiURL, "") // TODO : use nil for empty token
	req := controlplane.RstreamLoginRequest{Permissions: rstreamLoginPermissions, Source: resolveRstreamLoginSource()}
	res, err := client.CreateRstreamLogin(ctx, req)
	if err != nil {
		return err
	}
	if res.RequestID == "" || res.RequestSecret == "" || res.URL == "" {
		return errors.New("rstream login response is invalid")
	}
	fmt.Fprintln(os.Stdout, "Open this URL in your browser to continue login:")
	fmt.Fprintln(os.Stdout, res.URL)
	if err := openBrowser(res.URL); err != nil {
		fmt.Fprintln(os.Stderr, "Unable to open the browser automatically.")
	}
	fmt.Fprintln(os.Stdout, "Waiting for approval...")
	token, err := waitForRstreamLoginToken(ctx, client, res)
	if err != nil {
		return err
	}
	return storeToken(ctx, path, cfg, apiURL, token)
}

func waitForRstreamLoginToken(ctx context.Context, client *controlplane.Client, res controlplane.RstreamLoginResponse) (string, error) {
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
				return "", errors.New("login expired")
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
