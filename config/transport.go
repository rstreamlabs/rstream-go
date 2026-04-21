// See LICENSE file in the project root for license information.

package config

import "github.com/rstreamlabs/rstream-go"

type TransportConfig struct {
	Bind     *BindConfig  `yaml:"bind,omitempty"`
	IPFamily string       `yaml:"ipFamily,omitempty"`
	DNS      *DNSConfig   `yaml:"dns,omitempty"`
	MPTCP    *bool        `yaml:"mptcp,omitempty"`
	Proxy    *ProxyConfig `yaml:"proxy,omitempty"`
	UseQUIC  *bool        `yaml:"useQuic,omitempty"`
}

type BindConfig struct {
	Mode      string `yaml:"mode,omitempty"`
	Interface string `yaml:"interface,omitempty"`
	Address   string `yaml:"address,omitempty"`
}

type DNSConfig struct {
	Override string `yaml:"override,omitempty"`
}

type ProxyConfig struct {
	HTTP     string            `yaml:"http,omitempty"`
	Username string            `yaml:"username,omitempty"`
	Password string            `yaml:"password,omitempty"`
	Headers  map[string]string `yaml:"headers,omitempty"`
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
		if base.Proxy != nil {
			proxyCopy := *base.Proxy
			if base.Proxy.Headers != nil {
				headers := make(map[string]string, len(base.Proxy.Headers))
				for k, v := range base.Proxy.Headers {
					headers[k] = v
				}
				proxyCopy.Headers = headers
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
	}
	if override.MPTCP != nil {
		out.MPTCP = override.MPTCP
	}
	if override.UseQUIC != nil {
		out.UseQUIC = override.UseQUIC
	}
	if override.Proxy != nil {
		if out.Proxy == nil {
			out.Proxy = &ProxyConfig{}
		}
		if override.Proxy.HTTP != "" {
			out.Proxy.HTTP = override.Proxy.HTTP
		}
		if override.Proxy.Username != "" {
			out.Proxy.Username = override.Proxy.Username
		}
		if override.Proxy.Password != "" {
			out.Proxy.Password = override.Proxy.Password
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
	if cfg == nil {
		return nil
	}
	// If QUIC is requested, build a QUICTransport (no HTTP proxy support for QUIC).
	if cfg.UseQUIC != nil && *cfg.UseQUIC {
		var t rstream.QUICTransport
		set := false
		if cfg.Bind != nil {
			switch cfg.Bind.Mode {
			case "address", "":
				if cfg.Bind.Address != "" {
					t.LocalAddr = rstream.StringPtr(cfg.Bind.Address)
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
		if !set {
			return &rstream.QUICTransport{}
		}
		return &t
	}
	// TLS transport (default).
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
	if cfg.MPTCP != nil {
		transport.MPTCPEnabled = cfg.MPTCP
		set = true
	}
	if cfg.Proxy != nil {
		if cfg.Proxy.HTTP != "" {
			transport.ProxyHTTP = rstream.StringPtr(cfg.Proxy.HTTP)
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
		if len(cfg.Proxy.Headers) > 0 {
			transport.ProxyHTTPHeaders = make(map[string]string, len(cfg.Proxy.Headers))
			for k, v := range cfg.Proxy.Headers {
				transport.ProxyHTTPHeaders[k] = v
			}
			set = true
		}
	}
	if !set {
		return nil
	}
	return &transport
}
