// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

type resolvedRuntime struct {
	ConfigPath string
	Config     config.Config
	Resolved   config.Resolved
}

func resolveConfigPath(cmd *cobra.Command) (string, error) {
	flagPath, _ := cmd.Flags().GetString("config")
	if flagPath != "" {
		return flagPath, nil
	}
	if envPath := config.ReadEnv().ConfigPath; envPath != "" {
		return envPath, nil
	}
	return config.DefaultConfigPath()
}

func loadConfig(cmd *cobra.Command) (string, config.Config, error) {
	path, err := resolveConfigPath(cmd)
	if err != nil {
		return "", config.Config{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", config.Config{}, err
	}
	return path, cfg, nil
}

func resolveAPIURL(cmd *cobra.Command, cfg config.Config) (string, error) {
	flagAPIURL, _ := cmd.Flags().GetString("api-url")
	if flagAPIURL != "" {
		return config.NormalizeAPIURL(flagAPIURL), nil
	}
	if env := config.ReadEnv().APIURL; env != "" {
		return env, nil
	}
	return config.DefaultAPIURL(), nil
}

func resolveRuntime(cmd *cobra.Command, requireEngine, requireToken bool) (*resolvedRuntime, error) {
	path, cfg, err := loadConfig(cmd)
	if err != nil {
		return nil, err
	}
	flagAPIURL, _ := cmd.Flags().GetString("api-url")
	flagContext, _ := cmd.Flags().GetString("context")
	flagRegion, _ := cmd.Flags().GetString("region")
	flagTunnelTransport, _ := cmd.Flags().GetString("tunnel-transport")
	env := config.ReadEnv()
	input := config.ResolveInput{
		Config:                 cfg,
		FlagAPIURL:             flagAPIURL,
		FlagContext:            flagContext,
		FlagRegion:             flagRegion,
		EnvAPIURL:              env.APIURL,
		EnvContext:             env.Context,
		EnvEngine:              env.Engine,
		EnvToken:               env.Token,
		EnvMTLSCert:            env.MTLSCert,
		EnvMTLSKey:             env.MTLSKey,
		EnvRegion:              env.Region,
		FlagTunnelTransport:    flagTunnelTransport,
		EnvTunnelTransport:     env.TunnelTransport,
		EnvUseQUIC:             env.UseQUIC,
		EnvControlPlaneHeaders: env.ControlPlaneHeaders,
		RequireEngine:          requireEngine,
		RequireToken:           requireToken,
		ResolveToken:           true,
	}
	resolved, err := config.Resolve(input)
	if err != nil {
		return nil, err
	}
	if requireEngine && resolved.Region != "" {
		if err := resolveRuntimeRegion(cmd, cfg, &resolved); err != nil {
			return nil, err
		}
	}
	return &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: resolved}, nil
}

func resolveRuntimeRegion(cmd *cobra.Command, cfg config.Config, resolved *config.Resolved) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	return resolveRuntimeRegionContext(ctx, cfg, resolved)
}

func resolveRuntimeRegionContext(ctx context.Context, cfg config.Config, resolved *config.Resolved) error {
	if resolved.Context == nil || resolved.Context.ProjectEndpoint == "" {
		return errors.New("managed project endpoint is required for region selection")
	}
	token := resolved.Token
	if token == "" {
		var err error
		token, err = resolveControlPlaneToken(cfg, resolved.APIURL)
		if err != nil {
			return err
		}
	}
	client := controlplane.NewClient(resolved.APIURL, token, controlplane.WithHeaders(resolved.ControlPlaneHeaders))
	if err := client.RequireToken(); err != nil {
		return err
	}
	project, err := client.ResolveProjectByEndpoint(ctx, resolved.Context.ProjectEndpoint)
	if err != nil {
		return mapControlPlaneError(err)
	}
	return config.ResolveProjectRegion(resolved, project)
}

func resolveControlPlane(cmd *cobra.Command, requireToken bool) (*resolvedRuntime, error) {
	path, cfg, err := loadConfig(cmd)
	if err != nil {
		return nil, err
	}
	flagAPIURL, _ := cmd.Flags().GetString("api-url")
	flagContext, _ := cmd.Flags().GetString("context")
	env := config.ReadEnv()
	input := config.ResolveInput{
		Config:                 cfg,
		FlagAPIURL:             flagAPIURL,
		FlagContext:            flagContext,
		EnvAPIURL:              env.APIURL,
		EnvContext:             env.Context,
		EnvToken:               env.Token,
		EnvControlPlaneHeaders: env.ControlPlaneHeaders,
		IgnoreDefaultContext:   true,
		RequireToken:           requireToken,
		ResolveToken:           true,
	}
	resolved, err := config.Resolve(input)
	if err != nil {
		return nil, err
	}
	return &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: resolved}, nil
}

func newClientFromResolved(resolved config.Resolved) (*rstream.Client, error) {
	return config.NewClientFromResolved(resolved)
}

func setEnvironmentToken(env *config.Environment, token string) error {
	if token == "" {
		return errors.New("token is empty")
	}
	if env.Auth == nil {
		env.Auth = &config.Auth{}
	}
	if env.Auth.Token == nil {
		env.Auth.Token = &config.Token{}
	}
	env.Auth.Token.Storage = &config.TokenStorage{
		Kind:  config.TokenStorageInline,
		Value: token,
	}
	return nil
}

func setEnvironmentTokenStorage(env *config.Environment, storage config.TokenStorage) {
	if env.Auth == nil {
		env.Auth = &config.Auth{}
	}
	if env.Auth.Token == nil {
		env.Auth.Token = &config.Token{}
	}
	env.Auth.Token.Storage = &storage
}

func clearEnvironmentToken(env *config.Environment) {
	if env == nil || env.Auth == nil || env.Auth.Token == nil {
		return
	}
	env.Auth.Token = nil
	if env.Auth != nil && env.Auth.Token == nil && env.Auth.MTLS == nil {
		env.Auth = nil
	}
}

func validateOutputMode(value string, allowed ...string) error {
	for _, v := range allowed {
		if value == v {
			return nil
		}
	}
	return fmt.Errorf("invalid --output %q (valid: %s)", value, allowed)
}
