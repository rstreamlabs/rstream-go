// See LICENSE file in the project root for license information.

// Package tunnelconfig resolves tunnel options independently of their CLI,
// YAML or Docker representation. Adapters preserve presence with pointers.
package tunnelconfig

import (
	"fmt"
	"strings"

	"github.com/rstreamlabs/rstream-go"
)

// Bool resolves the positive and inverse spellings of one explicit option.
func Bool(value, inverse *bool) (*bool, error) {
	if value != nil && inverse != nil {
		return nil, fmt.Errorf("positive and negative forms of an option cannot be combined")
	}
	if value != nil {
		return rstream.BoolPtr(*value), nil
	}
	if inverse != nil {
		return rstream.BoolPtr(!*inverse), nil
	}
	return nil, nil
}

// Resolve normalizes and validates a complete set of tunnel options.
// Command-specific defaults must be applied by the caller before resolution.
func Resolve(props rstream.TunnelProperties) (rstream.TunnelProperties, error) {
	if props.Type != nil {
		value, e := ParseTunnelType(string(*props.Type))
		if e != nil {
			return props, e
		}
		props.Type = &value
	}
	if props.Protocol != nil {
		value, e := ParseProtocol(string(*props.Protocol))
		if e != nil {
			return props, e
		}
		props.Protocol = &value
	}
	if props.HTTPVersion != nil {
		value, e := ParseHTTPVersion(string(*props.HTTPVersion))
		if e != nil {
			return props, e
		}
		props.HTTPVersion = &value
	}
	if props.TLSMode != nil {
		value, e := ParseTLSMode(string(*props.TLSMode))
		if e != nil {
			return props, e
		}
		props.TLSMode = &value
	}
	if props.TLSMinVersion != nil {
		value, e := ParseTLSMinVersion(*props.TLSMinVersion)
		if e != nil {
			return props, e
		}
		props.TLSMinVersion = &value
	}
	if props.Hostname != nil {
		value := strings.TrimSpace(*props.Hostname)
		if value == "" {
			return props, fmt.Errorf("host must not be empty")
		}
		props.Hostname = &value
	}
	if props.Protocol != nil && *props.Protocol != rstream.ProtocolHTTP && (props.HTTPVersion != nil || props.HTTPUseTLS != nil || props.TokenAuth != nil || props.RstreamAuth != nil || props.ChallengeMode != nil) {
		return props, fmt.Errorf("http settings require protocol %q", rstream.ProtocolHTTP)
	}
	if err := normalizePublishedTCP(&props); err != nil {
		return props, err
	}
	datagram := props.Type != nil && *props.Type == rstream.TunnelTypeDatagram
	datagramProtocol := props.Protocol != nil && (*props.Protocol == rstream.ProtocolDTLS || *props.Protocol == rstream.ProtocolQUIC || (*props.Protocol == rstream.ProtocolHTTP && props.HTTPVersion != nil && *props.HTTPVersion == rstream.HTTP3))
	if props.DatagramGuaranteedDelivery != nil && !(datagram || props.Type == nil && datagramProtocol) {
		return props, fmt.Errorf("datagram-guaranteed-delivery requires a datagram tunnel")
	}
	if datagram && props.Publish != nil && *props.Publish && !datagramProtocol && (props.Protocol == nil || *props.Protocol != rstream.ProtocolWebTTY) {
		return props, fmt.Errorf("published datagram tunnel requires dtls, quic, HTTP/3, or managed WebTTY")
	}
	return props, nil
}

func normalizePublishedTCP(props *rstream.TunnelProperties) error {
	if props.Protocol == nil || *props.Protocol != rstream.ProtocolTCP {
		if props.Port != nil {
			return fmt.Errorf("port requires protocol %q", rstream.ProtocolTCP)
		}
		return nil
	}
	if props.Type != nil && *props.Type != rstream.TunnelTypeBytestream {
		return fmt.Errorf("protocol %q requires a bytestream tunnel", rstream.ProtocolTCP)
	}
	if props.Port != nil && (*props.Port == 0 || *props.Port > 65535) {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if props.Publish != nil && !*props.Publish {
		return fmt.Errorf("protocol %q requires a published tunnel", rstream.ProtocolTCP)
	}
	if props.Hostname != nil {
		return fmt.Errorf("protocol %q does not accept host", rstream.ProtocolTCP)
	}
	if props.TLSMode != nil || len(props.TLSALPNs) > 0 || props.TLSMinVersion != nil || len(props.TLSCiphers) > 0 || props.MTLSAuth != nil || props.HTTPVersion != nil || props.HTTPUseTLS != nil || props.UpstreamTLS != nil || props.TokenAuth != nil || props.RstreamAuth != nil || props.ChallengeMode != nil || props.DatagramGuaranteedDelivery != nil {
		return fmt.Errorf("protocol %q does not accept HTTP, TLS, edge authentication, or datagram delivery options", rstream.ProtocolTCP)
	}
	tunnelType := rstream.TunnelTypeBytestream
	props.Type = &tunnelType
	props.Publish = rstream.BoolPtr(true)
	return nil
}
