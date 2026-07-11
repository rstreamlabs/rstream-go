// See LICENSE file in the project root for license information.

package rstream

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"

	enginetls "github.com/rstreamlabs/rstream-go/internal/ech"
)

var defaultECHResolver = enginetls.NewResolver()

var lookupECHConfigList = func(ctx context.Context, target enginetls.Target, opts enginetls.ResolverOptions) ([]byte, error) {
	return defaultECHResolver.LookupConfigList(ctx, target, opts)
}

func dialWithECH(ctx context.Context, transport Dialer, addr string, baseCfg *tls.Config) (net.Conn, error) {
	if baseCfg == nil || len(baseCfg.EncryptedClientHelloConfigList) > 0 {
		return transport.Dial(ctx, addr, baseCfg)
	}
	if baseCfg.MaxVersion != 0 && baseCfg.MaxVersion < tls.VersionTLS13 {
		return transport.Dial(ctx, addr, baseCfg)
	}
	serverName := baseCfg.ServerName
	if serverName == "" {
		host, _, err := splitHostPort(addr)
		if err != nil || host == nil {
			return transport.Dial(ctx, addr, baseCfg)
		}
		serverName = *host
	}
	opts := echResolverOptions(transport)
	target := enginetls.Target{
		Address:    addr,
		ServerName: serverName,
		NextProtos: cloneNextProtos(baseCfg.NextProtos),
	}
	configList, err := lookupECHConfigList(ctx, target, opts)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return transport.Dial(ctx, addr, baseCfg)
	}
	if len(configList) == 0 {
		return transport.Dial(ctx, addr, baseCfg)
	}
	echCfg := newECHTLSConfig(baseCfg, configList)
	conn, err := transport.Dial(ctx, addr, echCfg)
	if err == nil {
		return conn, nil
	}
	var rejection *tls.ECHRejectionError
	if !errors.As(err, &rejection) {
		return nil, err
	}
	if len(rejection.RetryConfigList) > 0 && !bytes.Equal(rejection.RetryConfigList, configList) {
		_ = defaultECHResolver.RememberConfigList(target, opts, rejection.RetryConfigList)
		retryCfg := newECHTLSConfig(baseCfg, rejection.RetryConfigList)
		retryConn, retryErr := transport.Dial(ctx, addr, retryCfg)
		if retryErr == nil {
			return retryConn, nil
		}
		return nil, retryErr
	}
	return nil, err
}

func newECHTLSConfig(baseCfg *tls.Config, configList []byte) *tls.Config {
	cfg := baseCfg.Clone()
	cfg.EncryptedClientHelloConfigList = append([]byte(nil), configList...)
	if cfg.MinVersion == 0 || cfg.MinVersion < tls.VersionTLS13 {
		cfg.MinVersion = tls.VersionTLS13
	}
	if cfg.InsecureSkipVerify && cfg.EncryptedClientHelloRejectionVerify == nil {
		cfg.EncryptedClientHelloRejectionVerify = func(tls.ConnectionState) error { return nil }
	}
	return cfg
}

func echResolverOptions(transport Dialer) enginetls.ResolverOptions {
	switch t := transport.(type) {
	case *Transport:
		return enginetls.ResolverOptions{
			DNSOverride:   stringValue(t.DNSOverride),
			DNSOverTLS:    boolValue(t.DNSOverTLS),
			DNSServerName: stringValue(t.DNSServerName),
			DNSSECEnabled: boolValue(t.DNSSECEnabled),
			ForceIPv4:     boolValue(t.ForceIPv4),
			ForceIPv6:     boolValue(t.ForceIPv6),
		}
	case *QUICTransport:
		return enginetls.ResolverOptions{
			DNSOverride:   stringValue(t.DNSOverride),
			DNSOverTLS:    boolValue(t.DNSOverTLS),
			DNSServerName: stringValue(t.DNSServerName),
			DNSSECEnabled: boolValue(t.DNSSECEnabled),
			ForceIPv4:     boolValue(t.ForceIPv4),
			ForceIPv6:     boolValue(t.ForceIPv6),
		}
	case *AutoTransport:
		if t == nil {
			return enginetls.ResolverOptions{}
		}
		if selected := t.SelectedTransport(); selected != nil {
			return echResolverOptions(selected)
		}
		if t.QUIC != nil {
			return echResolverOptions(t.QUIC)
		}
		if t.TLS != nil {
			return echResolverOptions(t.TLS)
		}
		return enginetls.ResolverOptions{}
	default:
		return enginetls.ResolverOptions{}
	}
}

func boolValue(v *bool) bool {
	return v != nil && *v
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func cloneNextProtos(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
