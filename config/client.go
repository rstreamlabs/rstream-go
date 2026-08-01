// See LICENSE file in the project root for license information.

package config

import (
	"context"
	"errors"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

type ClientEnvOptions struct {
	ConfigPath      string
	APIURL          string
	Context         string
	Engine          string
	Token           string
	MTLSCert        string
	MTLSKey         string
	Region          string
	RequireEngine   bool
	RequireToken    bool
	TunnelTransport string
}

type ClientResolution struct {
	ConfigPath string
	Config     Config
	Resolved   Resolved
}

func ResolveFromEnv(opts ClientEnvOptions) (ClientResolution, error) {
	env := ReadEnv()
	path := strings.TrimSpace(opts.ConfigPath)
	if path == "" {
		path = env.ConfigPath
	}
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return ClientResolution{}, err
		}
	}
	cfg, err := Load(path)
	if err != nil {
		return ClientResolution{}, err
	}
	input := ResolveInput{
		Config:                 cfg,
		FlagAPIURL:             strings.TrimSpace(opts.APIURL),
		FlagContext:            strings.TrimSpace(opts.Context),
		FlagEngine:             strings.TrimSpace(opts.Engine),
		FlagToken:              strings.TrimSpace(opts.Token),
		FlagMTLSCert:           strings.TrimSpace(opts.MTLSCert),
		FlagMTLSKey:            strings.TrimSpace(opts.MTLSKey),
		FlagRegion:             strings.TrimSpace(opts.Region),
		EnvAPIURL:              env.APIURL,
		EnvContext:             env.Context,
		EnvEngine:              env.Engine,
		EnvToken:               env.Token,
		EnvMTLSCert:            env.MTLSCert,
		EnvMTLSKey:             env.MTLSKey,
		EnvRegion:              env.Region,
		FlagTunnelTransport:    strings.TrimSpace(opts.TunnelTransport),
		EnvTunnelTransport:     env.TunnelTransport,
		EnvUseQUIC:             env.UseQUIC,
		EnvControlPlaneHeaders: env.ControlPlaneHeaders,
		RequireEngine:          opts.RequireEngine,
		RequireToken:           opts.RequireToken,
		ResolveToken:           true,
	}
	resolved, err := Resolve(input)
	if err != nil {
		return ClientResolution{}, err
	}
	return ClientResolution{ConfigPath: path, Config: cfg, Resolved: resolved}, nil
}

func NewClientFromEnv() (*rstream.Client, error) {
	return NewClientFromEnvOptions(ClientEnvOptions{RequireEngine: true})
}

func NewClientFromEnvOptions(opts ClientEnvOptions) (*rstream.Client, error) {
	return NewClientFromEnvOptionsContext(context.Background(), opts)
}

func NewClientFromEnvOptionsContext(ctx context.Context, opts ClientEnvOptions) (*rstream.Client, error) {
	resolution, err := ResolveFromEnv(opts)
	if err != nil {
		return nil, err
	}
	if err := resolveClientRegion(ctx, &resolution.Resolved); err != nil {
		return nil, err
	}
	return NewClientFromResolved(resolution.Resolved)
}

func resolveClientRegion(ctx context.Context, resolved *Resolved) error {
	if resolved.Region == "" {
		return nil
	}
	if resolved.Context == nil || strings.TrimSpace(resolved.Context.ProjectEndpoint) == "" {
		return errors.New("managed project endpoint is required for region selection")
	}
	client := controlplane.NewClient(resolved.APIURL, resolved.Token, controlplane.WithHeaders(resolved.ControlPlaneHeaders))
	if err := client.RequireToken(); err != nil {
		return err
	}
	project, err := client.ResolveProjectByEndpoint(ctx, resolved.Context.ProjectEndpoint)
	if err != nil {
		return err
	}
	return ResolveProjectRegion(resolved, project)
}

func ResolveProjectRegion(resolved *Resolved, project controlplane.Project) error {
	if resolved == nil {
		return errors.New("resolved configuration is required")
	}
	engine, err := project.EngineAddressForRegion(resolved.Region)
	if err != nil {
		return err
	}
	stableDomainEngine := project.EngineAddress()
	if stableDomainEngine == "" {
		stableDomainEngine = engine
	}
	resolved.Engine = engine
	resolved.StableDomainEngine = stableDomainEngine
	return nil
}

func NewClientFromResolved(resolved Resolved) (*rstream.Client, error) {
	options := rstream.ClientOptions{
		Engine: resolved.Engine,
		Token:  resolved.Token,
	}
	if resolved.Transport != nil {
		options.Transport = resolved.Transport
	}
	if resolved.TLSClientConfig != nil {
		options.TLSClientConfig = resolved.TLSClientConfig
	}
	if resolved.Token == "" {
		options.NoToken = true
	}
	return rstream.NewClient(options)
}
