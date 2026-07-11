// See LICENSE file in the project root for license information.

package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/rstreamlabs/rstream-go"
)

type TransportConfig struct {
	Bind     *BindConfig  `yaml:"bind,omitempty"`
	IPFamily string       `yaml:"ipFamily,omitempty"`
	DNS      *DNSConfig   `yaml:"dns,omitempty"`
	MPTCP    *bool        `yaml:"mptcp,omitempty"`
	TLS      *TLSConfig   `yaml:"tls,omitempty"`
	Proxy    *ProxyConfig `yaml:"proxy,omitempty"`
	Mode     string       `yaml:"mode,omitempty"`
	// UseQUIC is the legacy transport selector. Prefer Mode.
	UseQUIC *bool `yaml:"useQuic,omitempty"`
}

type BindConfig struct {
	Mode      string `yaml:"mode,omitempty"`
	Interface string `yaml:"interface,omitempty"`
	Address   string `yaml:"address,omitempty"`
}

type DNSConfig struct {
	Override   string `yaml:"override,omitempty"`
	TLS        *bool  `yaml:"tls,omitempty"`
	ServerName string `yaml:"serverName,omitempty"`
	DNSSEC     *bool  `yaml:"dnssec,omitempty"`
}

type ProxyConfig struct {
	HTTP            string            `yaml:"http,omitempty"`
	SOCKS5          string            `yaml:"socks5,omitempty"`
	Username        string            `yaml:"username,omitempty"`
	Password        string            `yaml:"password,omitempty"`
	Headers         map[string]string `yaml:"headers,omitempty"`
	FromEnvironment *bool             `yaml:"fromEnvironment,omitempty"`
	TLS             *ProxyTLSConfig   `yaml:"tls,omitempty"`
}

type TLSConfig struct {
	CAFile             string `yaml:"caFile,omitempty"`
	ServerName         string `yaml:"serverName,omitempty"`
	InsecureSkipVerify *bool  `yaml:"insecureSkipVerify,omitempty"`
}

type ProxyTLSConfig struct {
	CAFile             string `yaml:"caFile,omitempty"`
	ServerName         string `yaml:"serverName,omitempty"`
	InsecureSkipVerify *bool  `yaml:"insecureSkipVerify,omitempty"`
}

func MergeTransport(base, override *TransportConfig) *TransportConfig {
	if base == nil && override == nil {
		return nil
	}
	var out TransportConfig
	if base != nil {
		out = *base
		if base.Bind != nil {
			bindCopy := *base.Bind
			out.Bind = &bindCopy
		}
		if base.DNS != nil {
			dnsCopy := *base.DNS
			out.DNS = &dnsCopy
		}
		if base.TLS != nil {
			tlsCopy := *base.TLS
			out.TLS = &tlsCopy
		}
		if base.Proxy != nil {
			proxyCopy := *base.Proxy
			if base.Proxy.Headers != nil {
				headers := make(map[string]string, len(base.Proxy.Headers))
				for k, v := range base.Proxy.Headers {
					headers[k] = v
				}
				proxyCopy.Headers = headers
			}
			if base.Proxy.TLS != nil {
				tlsCopy := *base.Proxy.TLS
				proxyCopy.TLS = &tlsCopy
			}
			out.Proxy = &proxyCopy
		}
	}
	if override == nil {
		return &out
	}
	if override.Bind != nil {
		if out.Bind == nil {
			out.Bind = &BindConfig{}
		}
		if override.Bind.Mode != "" {
			out.Bind.Mode = override.Bind.Mode
		}
		if override.Bind.Interface != "" {
			out.Bind.Interface = override.Bind.Interface
		}
		if override.Bind.Address != "" {
			out.Bind.Address = override.Bind.Address
		}
	}
	if override.IPFamily != "" {
		out.IPFamily = override.IPFamily
	}
	if override.DNS != nil {
		if out.DNS == nil {
			out.DNS = &DNSConfig{}
		}
		if override.DNS.Override != "" {
			out.DNS.Override = override.DNS.Override
		}
		if override.DNS.TLS != nil {
			out.DNS.TLS = override.DNS.TLS
		}
		if override.DNS.ServerName != "" {
			out.DNS.ServerName = override.DNS.ServerName
		}
		if override.DNS.DNSSEC != nil {
			out.DNS.DNSSEC = override.DNS.DNSSEC
		}
	}
	if override.MPTCP != nil {
		out.MPTCP = override.MPTCP
	}
	if override.Mode != "" {
		out.Mode = override.Mode
		out.UseQUIC = nil
	}
	if override.TLS != nil {
		if out.TLS == nil {
			out.TLS = &TLSConfig{}
		}
		if override.TLS.CAFile != "" {
			out.TLS.CAFile = override.TLS.CAFile
		}
		if override.TLS.ServerName != "" {
			out.TLS.ServerName = override.TLS.ServerName
		}
		if override.TLS.InsecureSkipVerify != nil {
			out.TLS.InsecureSkipVerify = override.TLS.InsecureSkipVerify
		}
	}
	if override.UseQUIC != nil {
		out.UseQUIC = override.UseQUIC
		if override.Mode == "" {
			out.Mode = ""
		}
	}
	if override.Proxy != nil {
		if out.Proxy == nil {
			out.Proxy = &ProxyConfig{}
		}
		if override.Proxy.HTTP != "" {
			out.Proxy.HTTP = override.Proxy.HTTP
		}
		if override.Proxy.SOCKS5 != "" {
			out.Proxy.SOCKS5 = override.Proxy.SOCKS5
		}
		if override.Proxy.Username != "" {
			out.Proxy.Username = override.Proxy.Username
		}
		if override.Proxy.Password != "" {
			out.Proxy.Password = override.Proxy.Password
		}
		if override.Proxy.FromEnvironment != nil {
			out.Proxy.FromEnvironment = override.Proxy.FromEnvironment
		}
		if override.Proxy.TLS != nil {
			if out.Proxy.TLS == nil {
				out.Proxy.TLS = &ProxyTLSConfig{}
			}
			if override.Proxy.TLS.CAFile != "" {
				out.Proxy.TLS.CAFile = override.Proxy.TLS.CAFile
			}
			if override.Proxy.TLS.ServerName != "" {
				out.Proxy.TLS.ServerName = override.Proxy.TLS.ServerName
			}
			if override.Proxy.TLS.InsecureSkipVerify != nil {
				out.Proxy.TLS.InsecureSkipVerify = override.Proxy.TLS.InsecureSkipVerify
			}
		}
		if len(override.Proxy.Headers) > 0 {
			if out.Proxy.Headers == nil {
				out.Proxy.Headers = make(map[string]string)
			}
			for k, v := range override.Proxy.Headers {
				out.Proxy.Headers[k] = v
			}
		}
	}
	return &out
}

func FlattenTransport(cfg *TransportConfig) rstream.Dialer {
	transport, _ := FlattenTransportWithError(cfg)
	return transport
}

func FlattenTransportWithError(cfg *TransportConfig) (rstream.Dialer, error) {
	mode, err := transportMode(cfg)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &rstream.AutoTransport{}, nil
	}
	if mode == rstream.TunnelTransportModeAuto {
		tlsCfg := *cfg
		tlsCfg.Mode = string(rstream.TunnelTransportModeTLS)
		tlsCfg.UseQUIC = nil
		tlsDialer, err := FlattenTransportWithError(&tlsCfg)
		if err != nil {
			return nil, err
		}
		quicCfg := *cfg
		quicCfg.Mode = string(rstream.TunnelTransportModeQUIC)
		quicCfg.UseQUIC = nil
		quicDialer, err := FlattenTransportWithError(&quicCfg)
		if err != nil {
			return nil, err
		}
		tlsTransport, _ := tlsDialer.(*rstream.Transport)
		if tlsTransport == nil {
			tlsTransport = &rstream.Transport{}
		}
		quicTransport, _ := quicDialer.(*rstream.QUICTransport)
		if quicTransport == nil {
			quicTransport = &rstream.QUICTransport{}
		}
		return &rstream.AutoTransport{TLS: tlsTransport, QUIC: quicTransport}, nil
	}
	if err := validateProxyTLSConfig(cfg.Proxy); err != nil {
		return nil, err
	}
	if mode == rstream.TunnelTransportModeQUIC {
		var t rstream.QUICTransport
		set := false
		if cfg.Bind != nil {
			switch cfg.Bind.Mode {
			case "address", "":
				if cfg.Bind.Address != "" {
					t.LocalAddr = rstream.StringPtr(cfg.Bind.Address)
					set = true
				}
			case "interface":
				if cfg.Bind.Interface != "" {
					t.NetworkInterface = rstream.StringPtr(cfg.Bind.Interface)
					set = true
				}
			}
		}
		switch cfg.IPFamily {
		case "ipv4":
			t.ForceIPv4 = rstream.BoolPtr(true)
			set = true
		case "ipv6":
			t.ForceIPv6 = rstream.BoolPtr(true)
			set = true
		}
		if cfg.DNS != nil && cfg.DNS.Override != "" {
			t.DNSOverride = rstream.StringPtr(cfg.DNS.Override)
			set = true
		}
		if cfg.DNS != nil && cfg.DNS.TLS != nil {
			t.DNSOverTLS = cfg.DNS.TLS
			set = true
		}
		if cfg.DNS != nil && cfg.DNS.ServerName != "" {
			t.DNSServerName = rstream.StringPtr(cfg.DNS.ServerName)
			set = true
		}
		if cfg.DNS != nil && cfg.DNS.DNSSEC != nil {
			t.DNSSECEnabled = cfg.DNS.DNSSEC
			set = true
		}
		if cfg.Proxy != nil {
			if cfg.Proxy.HTTP != "" {
				t.ProxyHTTP = rstream.StringPtr(cfg.Proxy.HTTP)
				set = true
			}
			if cfg.Proxy.SOCKS5 != "" {
				t.ProxySOCKS5 = rstream.StringPtr(cfg.Proxy.SOCKS5)
				set = true
			}
			if cfg.Proxy.Username != "" {
				t.ProxyUsername = rstream.StringPtr(cfg.Proxy.Username)
				set = true
			}
			if cfg.Proxy.Password != "" {
				t.ProxyPassword = rstream.StringPtr(cfg.Proxy.Password)
				set = true
			}
			if cfg.Proxy.FromEnvironment != nil {
				t.ProxyFromEnvironment = cfg.Proxy.FromEnvironment
				set = true
			}
			if len(cfg.Proxy.Headers) > 0 {
				t.ProxyHTTPHeaders = make(map[string]string, len(cfg.Proxy.Headers))
				for k, v := range cfg.Proxy.Headers {
					t.ProxyHTTPHeaders[k] = v
				}
				set = true
			}
			proxyTLS, err := proxyTLSConfig(cfg.Proxy.TLS)
			if err != nil {
				return nil, err
			}
			if proxyTLS != nil {
				t.TLSProxyConfig = proxyTLS
				set = true
			}
		}
		if !set {
			return &rstream.QUICTransport{}, nil
		}
		return &t, nil
	}
	// Explicit TLS transport.
	var transport rstream.Transport
	set := false
	if cfg.Bind != nil {
		switch cfg.Bind.Mode {
		case "interface":
			if cfg.Bind.Interface != "" {
				transport.NetworkInterface = rstream.StringPtr(cfg.Bind.Interface)
				set = true
			}
		case "address", "":
			if cfg.Bind.Address != "" {
				transport.LocalAddr = rstream.StringPtr(cfg.Bind.Address)
				set = true
			}
		}
	}
	switch cfg.IPFamily {
	case "ipv4":
		transport.ForceIPv4 = rstream.BoolPtr(true)
		set = true
	case "ipv6":
		transport.ForceIPv6 = rstream.BoolPtr(true)
		set = true
	}
	if cfg.DNS != nil && cfg.DNS.Override != "" {
		transport.DNSOverride = rstream.StringPtr(cfg.DNS.Override)
		set = true
	}
	if cfg.DNS != nil && cfg.DNS.TLS != nil {
		transport.DNSOverTLS = cfg.DNS.TLS
		set = true
	}
	if cfg.DNS != nil && cfg.DNS.ServerName != "" {
		transport.DNSServerName = rstream.StringPtr(cfg.DNS.ServerName)
		set = true
	}
	if cfg.DNS != nil && cfg.DNS.DNSSEC != nil {
		transport.DNSSECEnabled = cfg.DNS.DNSSEC
		set = true
	}
	if cfg.MPTCP != nil {
		transport.MPTCPEnabled = cfg.MPTCP
		set = true
	}
	if cfg.Proxy != nil {
		if cfg.Proxy.HTTP != "" {
			transport.ProxyHTTP = rstream.StringPtr(cfg.Proxy.HTTP)
			set = true
		}
		if cfg.Proxy.SOCKS5 != "" {
			transport.ProxySOCKS5 = rstream.StringPtr(cfg.Proxy.SOCKS5)
			set = true
		}
		if cfg.Proxy.Username != "" {
			transport.ProxyUsername = rstream.StringPtr(cfg.Proxy.Username)
			set = true
		}
		if cfg.Proxy.Password != "" {
			transport.ProxyPassword = rstream.StringPtr(cfg.Proxy.Password)
			set = true
		}
		if cfg.Proxy.FromEnvironment != nil {
			transport.ProxyFromEnvironment = cfg.Proxy.FromEnvironment
			set = true
		}
		if len(cfg.Proxy.Headers) > 0 {
			transport.ProxyHTTPHeaders = make(map[string]string, len(cfg.Proxy.Headers))
			for k, v := range cfg.Proxy.Headers {
				transport.ProxyHTTPHeaders[k] = v
			}
			set = true
		}
		proxyTLS, err := proxyTLSConfig(cfg.Proxy.TLS)
		if err != nil {
			return nil, err
		}
		if proxyTLS != nil {
			transport.TLSProxyConfig = proxyTLS
			set = true
		}
	}
	if !set {
		return &rstream.Transport{}, nil
	}
	return &transport, nil
}

func transportMode(cfg *TransportConfig) (rstream.TunnelTransportMode, error) {
	if cfg == nil {
		return rstream.TunnelTransportModeAuto, nil
	}
	if cfg.Mode != "" {
		return rstream.ParseTunnelTransportMode(cfg.Mode)
	}
	if cfg.UseQUIC != nil {
		if *cfg.UseQUIC {
			return rstream.TunnelTransportModeQUIC, nil
		}
		return rstream.TunnelTransportModeTLS, nil
	}
	return rstream.TunnelTransportModeAuto, nil
}

func validateProxyTLSConfig(proxy *ProxyConfig) error {
	if proxy == nil || proxy.TLS == nil {
		return nil
	}
	if proxy.SOCKS5 != "" {
		return fmt.Errorf("proxy TLS configuration can only be used with proxy.http or proxy.fromEnvironment")
	}
	if proxy.HTTP == "" && !boolPtrValue(proxy.FromEnvironment) {
		return fmt.Errorf("proxy TLS configuration requires proxy.http or proxy.fromEnvironment")
	}
	return nil
}

func EngineTLSConfig(cfg *TransportConfig) (*tls.Config, error) {
	if cfg == nil || cfg.TLS == nil {
		return nil, nil
	}
	return tlsConfigFromSettings(cfg.TLS.CAFile, cfg.TLS.ServerName, cfg.TLS.InsecureSkipVerify, "engine")
}

func boolPtrValue(value *bool) bool {
	return value != nil && *value
}

func proxyTLSConfig(cfg *ProxyTLSConfig) (*tls.Config, error) {
	if cfg == nil {
		return nil, nil
	}
	return tlsConfigFromSettings(cfg.CAFile, cfg.ServerName, cfg.InsecureSkipVerify, "proxy")
}

func tlsConfigFromSettings(caFile string, serverName string, insecureSkipVerify *bool, label string) (*tls.Config, error) {
	out := &tls.Config{}
	set := false
	if caFile != "" {
		certs, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s CA file %q: %w", label, caFile, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(certs) {
			return nil, fmt.Errorf("%s CA file %q does not contain a valid PEM certificate", label, caFile)
		}
		out.RootCAs = pool
		set = true
	}
	if serverName != "" {
		out.ServerName = serverName
		set = true
	}
	if insecureSkipVerify != nil {
		out.InsecureSkipVerify = *insecureSkipVerify
		set = true
	}
	if !set {
		return nil, nil
	}
	return out, nil
}
