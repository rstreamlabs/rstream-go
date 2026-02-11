// See LICENSE file in the project root for license information.

package config

import (
	"strings"

	"github.com/rstreamlabs/rstream-go"
)

type ClientEnvOptions struct {
	ConfigPath    string
	APIURL        string
	Context       string
	Engine        string
	Token         string
	RequireEngine bool
	RequireToken  bool
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
		Config:        cfg,
		FlagAPIURL:    strings.TrimSpace(opts.APIURL),
		FlagContext:   strings.TrimSpace(opts.Context),
		FlagEngine:    strings.TrimSpace(opts.Engine),
		FlagToken:     strings.TrimSpace(opts.Token),
		EnvAPIURL:     env.APIURL,
		EnvContext:    env.Context,
		EnvEngine:     env.Engine,
		EnvToken:      env.Token,
		RequireEngine: opts.RequireEngine,
		RequireToken:  opts.RequireToken,
		ResolveToken:  true,
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
	resolution, err := ResolveFromEnv(opts)
	if err != nil {
		return nil, err
	}
	return NewClientFromResolved(resolution.Resolved)
}

func NewClientFromResolved(resolved Resolved) (*rstream.Client, error) {
	options := rstream.ClientOptions{
		Engine: resolved.Engine,
		Token:  resolved.Token,
	}
	if resolved.Transport != nil {
		options.Transport = resolved.Transport
	}
	if resolved.Token == "" {
		options.NoToken = true
	}
	return rstream.NewClient(options)
}
