// See LICENSE file in the project root for license information.

package runapply

import (
	"fmt"
	"os"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/configdecode"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/runmodel"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/tunnelconfig"
	"github.com/rstreamlabs/rstream-go/config"
	"gopkg.in/yaml.v3"
)

type FileConfig struct {
	Version  int                     `yaml:"version"`
	Tunnels  []TunnelEntry           `yaml:"tunnels"`
	Contexts map[string]ContextEntry `yaml:"contexts,omitempty"`
}

type TunnelEntry struct {
	Name    string      `yaml:"name"`
	Forward string      `yaml:"forward"`
	Context *ContextRef `yaml:"context,omitempty"`
	Tunnel  *TunnelSpec `yaml:"tunnel"`
}

type TunnelSpec struct {
	Publish                    *bool             `yaml:"publish,omitempty"`
	Labels                     map[string]string `yaml:"labels,omitempty"`
	Protocol                   *string           `yaml:"protocol,omitempty"`
	Type                       *string           `yaml:"type,omitempty"`
	Host                       *string           `yaml:"host,omitempty"`
	Port                       *uint32           `yaml:"port,omitempty"`
	AllowCrossRegionRouting    *bool             `yaml:"allowCrossRegionRouting,omitempty"`
	UpstreamTLS                *bool             `yaml:"upstreamTLS,omitempty"`
	DatagramGuaranteedDelivery *bool             `yaml:"datagramGuaranteedDelivery,omitempty"`
	TrustedIPs                 []string          `yaml:"trustedIPs,omitempty"`
	GeoIP                      []string          `yaml:"geoip,omitempty"`
	HTTP                       *HTTPSpec         `yaml:"http,omitempty"`
	TLS                        *TLSSpec          `yaml:"tls,omitempty"`
}

type HTTPSpec struct {
	Version *string       `yaml:"version,omitempty"`
	Auth    *HTTPAuthSpec `yaml:"auth,omitempty"`
	Gate    *HTTPGateSpec `yaml:"gate,omitempty"`
}

type HTTPAuthSpec struct {
	Token   *bool `yaml:"token,omitempty"`
	Rstream *bool `yaml:"rstream,omitempty"`
}

type HTTPGateSpec struct {
	Challenge *bool `yaml:"challenge,omitempty"`
}

type TLSSpec struct {
	Mode       *string  `yaml:"mode,omitempty"`
	MinVersion *string  `yaml:"minVersion,omitempty"`
	ALPNs      []string `yaml:"alpns,omitempty"`
	Ciphers    []string `yaml:"ciphers,omitempty"`
	MTLS       *bool    `yaml:"mtls,omitempty"`
}

type ContextEntry struct {
	External  bool                    `yaml:"external,omitempty"`
	Name      string                  `yaml:"name,omitempty"`
	Engine    string                  `yaml:"engine,omitempty"`
	Token     string                  `yaml:"token,omitempty"`
	Transport *config.TransportConfig `yaml:"transport,omitempty"`
}

type InlineContext struct {
	Engine    string                  `yaml:"engine,omitempty"`
	Token     string                  `yaml:"token,omitempty"`
	Transport *config.TransportConfig `yaml:"transport,omitempty"`
}

type ContextRef struct {
	Name   string
	Inline *InlineContext
}

func (c *ContextRef) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		if value.Tag == "!!null" {
			return nil
		}
		c.Name = strings.TrimSpace(value.Value)
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("context must be a string or object")
	}
	var inline InlineContext
	var fields map[string]any
	if err := value.Decode(&fields); err != nil {
		return err
	}
	data, err := yaml.Marshal(fields)
	if err != nil {
		return err
	}
	if err := configdecode.YAML(data, &inline); err != nil {
		return err
	}
	c.Inline = &inline
	return nil
}

type ResolvedContextLookup func(name string) (runmodel.ResolvedContext, error)

func LoadConfig(path string) (FileConfig, error) {
	var cfg FileConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	expanded := os.ExpandEnv(string(data))
	if err := configdecode.YAML([]byte(expanded), &cfg); err != nil {
		return cfg, fmt.Errorf("invalid run config YAML: %w", err)
	}
	if cfg.Version != 1 {
		return cfg, fmt.Errorf("unsupported version %d (expected 1)", cfg.Version)
	}
	if cfg.Tunnels == nil {
		return cfg, fmt.Errorf("tunnels is required")
	}
	for i, t := range cfg.Tunnels {
		if strings.TrimSpace(t.Name) == "" {
			return cfg, fmt.Errorf("tunnels[%d].name is required", i)
		}
		if strings.TrimSpace(t.Forward) == "" {
			return cfg, fmt.Errorf("tunnels[%d].forward is required", i)
		}
		if t.Tunnel == nil {
			return cfg, fmt.Errorf("tunnels[%d].tunnel is required", i)
		}
	}
	return cfg, nil
}

func DesiredTunnels(path string, fallback runmodel.ResolvedContext, lookup ResolvedContextLookup) ([]runmodel.DesiredTunnel, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	resolvedContexts, err := resolveNamedContexts(cfg.Contexts, lookup)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(cfg.Tunnels))
	out := make([]runmodel.DesiredTunnel, 0, len(cfg.Tunnels))
	for _, t := range cfg.Tunnels {
		name := strings.TrimSpace(t.Name)
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate tunnel name %q", name)
		}
		seen[name] = struct{}{}
		ctx, err := resolveTunnelContext(t.Context, resolvedContexts, fallback)
		if err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", name, err)
		}
		forward, err := runmodel.ParseForwardTarget(t.Forward, "localhost")
		if err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", name, err)
		}
		props, err := tunnelPropertiesFromSpec(t.Tunnel)
		if err != nil {
			return nil, fmt.Errorf("tunnel %q: %w", name, err)
		}
		props.Name = &name
		runmodel.ApplyManagedLabels(&props, "apply")
		out = append(out, runmodel.DesiredTunnel{
			Name:    name,
			Forward: forward,
			Context: ctx,
			Props:   props,
			Source:  "apply",
		})
	}
	return out, nil
}

func resolveNamedContexts(contexts map[string]ContextEntry, lookup ResolvedContextLookup) (map[string]runmodel.ResolvedContext, error) {
	if len(contexts) == 0 {
		return nil, nil
	}
	out := make(map[string]runmodel.ResolvedContext, len(contexts))
	for key, ctx := range contexts {
		name := strings.TrimSpace(key)
		if name == "" {
			return nil, fmt.Errorf("context name is required")
		}
		if ctx.External {
			lookupName := strings.TrimSpace(ctx.Name)
			if lookupName == "" {
				lookupName = name
			}
			if lookup == nil {
				return nil, fmt.Errorf("context %q requires external lookup", name)
			}
			resolved, err := lookup(lookupName)
			if err != nil {
				return nil, fmt.Errorf("context %q: %w", name, err)
			}
			resolved.Name = lookupName
			out[name] = resolved
			continue
		}
		resolved, err := resolveInlineContext(ctx.Engine, ctx.Token, ctx.Transport)
		if err != nil {
			return nil, fmt.Errorf("context %q: %w", name, err)
		}
		resolved.Name = name
		out[name] = resolved
	}
	return out, nil
}

func resolveTunnelContext(ref *ContextRef, named map[string]runmodel.ResolvedContext, fallback runmodel.ResolvedContext) (runmodel.ResolvedContext, error) {
	if ref == nil {
		if fallback.Engine == "" || !fallback.HasAuthentication() {
			return runmodel.ResolvedContext{}, fmt.Errorf("fallback context is not configured")
		}
		return fallback, nil
	}
	if ref.Inline != nil {
		resolved, err := resolveInlineContext(ref.Inline.Engine, ref.Inline.Token, ref.Inline.Transport)
		if err != nil {
			return runmodel.ResolvedContext{}, err
		}
		resolved.Name = "inline"
		return resolved, nil
	}
	if strings.TrimSpace(ref.Name) == "" {
		return runmodel.ResolvedContext{}, fmt.Errorf("context reference is empty")
	}
	if named == nil {
		return runmodel.ResolvedContext{}, fmt.Errorf("context %q not found", ref.Name)
	}
	ctx, ok := named[ref.Name]
	if !ok {
		return runmodel.ResolvedContext{}, fmt.Errorf("context %q not found", ref.Name)
	}
	return ctx, nil
}

func resolveInlineContext(engine, token string, transport *config.TransportConfig) (runmodel.ResolvedContext, error) {
	engine = strings.TrimSpace(engine)
	token = strings.TrimSpace(token)
	if engine == "" {
		return runmodel.ResolvedContext{}, fmt.Errorf("engine is required")
	}
	if token == "" {
		return runmodel.ResolvedContext{}, fmt.Errorf("token is required")
	}
	probe, err := config.FlattenTransportWithError(transport)
	if err != nil {
		return runmodel.ResolvedContext{}, err
	}
	if closer, ok := probe.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			return runmodel.ResolvedContext{}, fmt.Errorf("failed to close transport validation probe: %w", err)
		}
	}
	return runmodel.ResolvedContext{
		Engine:          engine,
		Token:           token,
		TransportConfig: config.MergeTransport(nil, transport),
	}, nil
}

func tunnelPropertiesFromSpec(spec *TunnelSpec) (rstream.TunnelProperties, error) {
	props := rstream.TunnelProperties{}
	if spec == nil {
		return props, fmt.Errorf("tunnel spec is required")
	}
	if spec.Publish != nil {
		props.Publish = spec.Publish
	}
	if spec.Type != nil {
		val, err := tunnelconfig.ParseTunnelType(*spec.Type)
		if err != nil {
			return props, err
		}
		props.Type = &val
	}
	if spec.Protocol != nil {
		val, err := tunnelconfig.ParseProtocol(*spec.Protocol)
		if err != nil {
			return props, err
		}
		props.Protocol = &val
	}
	if len(spec.Labels) > 0 {
		props.Labels = spec.Labels
	}
	if len(spec.GeoIP) > 0 {
		props.GeoIP = spec.GeoIP
	}
	if len(spec.TrustedIPs) > 0 {
		props.TrustedIPs = spec.TrustedIPs
	}
	props.Hostname = spec.Host
	if spec.Port != nil {
		props.Port = spec.Port
	}
	if spec.AllowCrossRegionRouting != nil {
		props.AllowCrossRegionRouting = spec.AllowCrossRegionRouting
	}
	if spec.UpstreamTLS != nil {
		props.UpstreamTLS = spec.UpstreamTLS
	}
	if spec.DatagramGuaranteedDelivery != nil {
		props.DatagramGuaranteedDelivery = spec.DatagramGuaranteedDelivery
	}
	if spec.HTTP != nil {
		if spec.HTTP.Version != nil {
			val, err := tunnelconfig.ParseHTTPVersion(*spec.HTTP.Version)
			if err != nil {
				return props, err
			}
			props.HTTPVersion = &val
		}
		if spec.HTTP.Auth != nil {
			if spec.HTTP.Auth.Token != nil {
				props.TokenAuth = spec.HTTP.Auth.Token
			}
			if spec.HTTP.Auth.Rstream != nil {
				props.RstreamAuth = spec.HTTP.Auth.Rstream
			}
		}
		if spec.HTTP.Gate != nil && spec.HTTP.Gate.Challenge != nil {
			props.ChallengeMode = spec.HTTP.Gate.Challenge
		}
	}
	if spec.TLS != nil {
		if spec.TLS.Mode != nil {
			val, err := tunnelconfig.ParseTLSMode(*spec.TLS.Mode)
			if err != nil {
				return props, err
			}
			props.TLSMode = &val
		}
		if spec.TLS.MinVersion != nil {
			val, err := tunnelconfig.ParseTLSMinVersion(*spec.TLS.MinVersion)
			if err != nil {
				return props, err
			}
			props.TLSMinVersion = &val
		}
		if len(spec.TLS.ALPNs) > 0 {
			props.TLSALPNs = spec.TLS.ALPNs
		}
		props.TLSCiphers = spec.TLS.Ciphers
		if spec.TLS.MTLS != nil {
			props.MTLSAuth = spec.TLS.MTLS
		}
	}
	return tunnelconfig.Resolve(props)
}
