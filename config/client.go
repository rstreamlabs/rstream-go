// See LICENSE file in the project root for license information.

package config

import (
	"crypto/tls"
	"strings"

	"github.com/rstreamlabs/rstream-go"
)

type ClientEnvOptions struct {
	ConfigPath    string
	APIURL        string
	Context       string
	Engine        string
	Token         string
	MTLSCert      string
	MTLSKey       string
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
		FlagMTLSCert:  strings.TrimSpace(opts.MTLSCert),
		FlagMTLSKey:   strings.TrimSpace(opts.MTLSKey),
		EnvAPIURL:     env.APIURL,
		EnvContext:    env.Context,
		EnvEngine:     env.Engine,
		EnvToken:      env.Token,
		EnvMTLSCert:   env.MTLSCert,
		EnvMTLSKey:    env.MTLSKey,
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
	if ReadEnv().UseQUIC {
		resolution.Resolved.Transport = promoteToQUICTransport(resolution.Resolved.Transport)
	}
	return NewClientFromResolved(resolution.Resolved)
}

func promoteToQUICTransport(transport rstream.Dialer) rstream.Dialer {
	switch t := transport.(type) {
	case nil:
		return &rstream.QUICTransport{}
	case *rstream.QUICTransport:
		return t
	case *rstream.Transport:
		return &rstream.QUICTransport{
			LocalAddr:            t.LocalAddr,
			NetworkInterface:     t.NetworkInterface,
			ForceIPv4:            t.ForceIPv4,
			ForceIPv6:            t.ForceIPv6,
			DNSOverride:          t.DNSOverride,
			DNSOverTLS:           t.DNSOverTLS,
			DNSServerName:        t.DNSServerName,
			DNSSECEnabled:        t.DNSSECEnabled,
			ProxyHTTP:            t.ProxyHTTP,
			ProxySOCKS5:          t.ProxySOCKS5,
			ProxyUsername:        t.ProxyUsername,
			ProxyPassword:        t.ProxyPassword,
			ProxyHTTPHeaders:     cloneHeaders(t.ProxyHTTPHeaders),
			TLSProxyConfig:       cloneTLSConfig(t.TLSProxyConfig),
			ProxyFromEnvironment: t.ProxyFromEnvironment,
		}
	default:
		return &rstream.QUICTransport{}
	}
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}

func cloneTLSConfig(cfg *tls.Config) *tls.Config {
	if cfg == nil {
		return nil
	}
	return cfg.Clone()
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
