// See LICENSE file in the project root for license information.

package rstream

import (
	"crypto/tls"
	"fmt"

	"github.com/rstreamlabs/rstream-go/internal/fipsprofile"
)

func validateFIPSClient(transport Dialer, tlsConfig *tls.Config) error {
	if !fipsprofile.BuildEnabled() {
		return nil
	}
	if err := fipsprofile.Require(); err != nil {
		return err
	}
	if err := validateFIPSTLSConfig(tlsConfig, "Engine TLS"); err != nil {
		return err
	}
	return validateFIPSDialer(transport)
}

func validateFIPSDialer(transport Dialer) error {
	if !fipsprofile.BuildEnabled() || isNilDialer(transport) {
		return nil
	}
	switch transport := transport.(type) {
	case *Transport:
		return validateFIPSTLSConfig(transport.TLSProxyConfig, "proxy TLS")
	case *AutoTransport:
		if transport == nil {
			return nil
		}
		if transport.TLS != nil {
			if err := validateFIPSTLSConfig(transport.TLS.TLSProxyConfig, "TLS transport proxy TLS"); err != nil {
				return err
			}
		}
		if transport.QUIC != nil {
			return validateFIPSQUICTransport(transport.QUIC)
		}
		return nil
	case *QUICTransport:
		return validateFIPSQUICTransport(transport)
	default:
		return fmt.Errorf("custom transport %T is not approved for the rstream FIPS profile", transport)
	}
}

func validateFIPSQUICTransport(transport *QUICTransport) error {
	if !fipsprofile.BuildEnabled() || transport == nil {
		return nil
	}
	if proxyValue(transport.ProxyHTTP) != "" || proxyValue(transport.ProxySOCKS5) != "" || boolValue(transport.ProxyFromEnvironment) {
		return fipsprofile.Unavailable("proxied QUIC transport")
	}
	return validateFIPSTLSConfig(transport.TLSProxyConfig, "QUIC transport proxy TLS")
}

func validateFIPSQUICConfig(config *tls.Config) error {
	if !fipsprofile.BuildEnabled() || config == nil {
		return nil
	}
	if config.MinVersion != 0 && config.MinVersion != tls.VersionTLS13 {
		return fmt.Errorf("QUIC requires TLS 1.3 in the rstream FIPS profile")
	}
	if config.MaxVersion != 0 && config.MaxVersion != tls.VersionTLS13 {
		return fmt.Errorf("QUIC requires TLS 1.3 in the rstream FIPS profile")
	}
	return nil
}

func validateFIPSTLSConfig(config *tls.Config, label string) error {
	if !fipsprofile.BuildEnabled() || config == nil {
		return nil
	}
	if config.InsecureSkipVerify {
		return fmt.Errorf("%s cannot disable certificate verification in the rstream FIPS profile", label)
	}
	if len(config.EncryptedClientHelloConfigList) > 0 {
		return fmt.Errorf("%s cannot enable ECH in the current rstream FIPS profile", label)
	}
	if config.MinVersion != 0 && config.MinVersion < tls.VersionTLS12 {
		return fmt.Errorf("%s minimum version must be TLS 1.2 or later in the rstream FIPS profile", label)
	}
	if config.MaxVersion != 0 && config.MaxVersion < tls.VersionTLS12 {
		return fmt.Errorf("%s maximum version must be TLS 1.2 or later in the rstream FIPS profile", label)
	}
	for _, curve := range config.CurvePreferences {
		switch curve {
		case tls.CurveP256, tls.CurveP384, tls.SecP256r1MLKEM768, tls.SecP384r1MLKEM1024:
		default:
			return fmt.Errorf("%s curve %s is not approved for the rstream FIPS profile", label, curve)
		}
	}
	for _, suite := range config.CipherSuites {
		switch suite {
		case tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384:
		default:
			return fmt.Errorf("%s cipher suite 0x%04x is not approved for the rstream FIPS profile", label, suite)
		}
	}
	return nil
}

func validateFIPSTunnelProperties(props TunnelProperties) error {
	if !fipsprofile.BuildEnabled() {
		return nil
	}
	isPublishedQUIC := props.Protocol != nil && *props.Protocol == ProtocolQUIC
	isPublishedHTTP3 := props.Protocol != nil && *props.Protocol == ProtocolHTTP && props.HTTPVersion != nil && *props.HTTPVersion == HTTP3
	if props.Type != nil && *props.Type == TunnelTypeDatagram && !isPublishedQUIC && !isPublishedHTTP3 {
		return fipsprofile.Unavailable("datagram tunnels")
	}
	if props.Protocol != nil {
		switch *props.Protocol {
		case ProtocolDTLS:
			return fipsprofile.Unavailable("DTLS tunnels")
		case ProtocolWebTTY:
			return fipsprofile.Unavailable("WebTTY tunnels")
		}
	}
	if props.HTTPVersion != nil && *props.HTTPVersion == HTTP3 && !isPublishedHTTP3 {
		return fipsprofile.Unavailable("published HTTP/3 tunnels")
	}
	return nil
}
