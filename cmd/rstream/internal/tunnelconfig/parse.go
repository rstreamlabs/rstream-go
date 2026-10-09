// See LICENSE file in the project root for license information.

package tunnelconfig

import (
	"fmt"
	"strings"

	"github.com/rstreamlabs/rstream-go"
)

func ParseTunnelType(val string) (rstream.TunnelType, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case string(rstream.TunnelTypeBytestream):
		return rstream.TunnelTypeBytestream, nil
	case string(rstream.TunnelTypeDatagram):
		return rstream.TunnelTypeDatagram, nil
	default:
		return "", fmt.Errorf("invalid tunnel type %q", val)
	}
}

func ParseProtocol(val string) (rstream.Protocol, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case string(rstream.ProtocolHTTP):
		return rstream.ProtocolHTTP, nil
	case string(rstream.ProtocolTLS):
		return rstream.ProtocolTLS, nil
	case string(rstream.ProtocolTCP):
		return rstream.ProtocolTCP, nil
	case string(rstream.ProtocolDTLS):
		return rstream.ProtocolDTLS, nil
	case string(rstream.ProtocolQUIC):
		return rstream.ProtocolQUIC, nil
	case string(rstream.ProtocolWebTTY):
		return rstream.ProtocolWebTTY, nil
	default:
		return "", fmt.Errorf("invalid protocol %q", val)
	}
}

func ParseHTTPVersion(val string) (rstream.HTTPVersion, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case string(rstream.HTTP1_1):
		return rstream.HTTP1_1, nil
	case string(rstream.HTTP2):
		return rstream.HTTP2, nil
	case string(rstream.HTTP3):
		return rstream.HTTP3, nil
	default:
		return "", fmt.Errorf("invalid http version %q", val)
	}
}

func ParseTLSMode(val string) (rstream.TLSMode, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case string(rstream.TLSModeTerminated):
		return rstream.TLSModeTerminated, nil
	case string(rstream.TLSModePassthrough):
		return rstream.TLSModePassthrough, nil
	default:
		return "", fmt.Errorf("invalid tls mode %q", val)
	}
}

func ParseTLSMinVersion(val string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "tls1.2", "tls1.3":
		return strings.ToLower(strings.TrimSpace(val)), nil
	default:
		return "", fmt.Errorf("invalid tls min version %q", val)
	}
}
