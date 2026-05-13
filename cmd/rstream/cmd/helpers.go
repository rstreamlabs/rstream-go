// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/spf13/cobra"
)

func getStringPtr(cmd *cobra.Command, name string) *string {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, _ := cmd.Flags().GetString(name)
		return &val
	}
	return nil
}

func getBoolPtr(cmd *cobra.Command, name string) *bool {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, _ := cmd.Flags().GetBool(name)
		return &val
	}
	return nil
}

func getInt64Ptr(cmd *cobra.Command, name string) *int64 {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, _ := cmd.Flags().GetInt64(name)
		return &val
	}
	return nil
}

func getStringArrayMap(cmd *cobra.Command, name string) map[string]string {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		arr, _ := cmd.Flags().GetStringArray(name)
		m := make(map[string]string)
		for _, kv := range arr {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				m[parts[0]] = parts[1]
			}
		}
		if len(m) > 0 {
			return m
		}
	}
	return nil
}

func getStringSlice(cmd *cobra.Command, name string) []string {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, err := cmd.Flags().GetStringSlice(name)
		if err == nil {
			out := make([]string, 0, len(val))
			for _, item := range val {
				item = strings.TrimSpace(item)
				if item != "" {
					out = append(out, item)
				}
			}
			if len(out) > 0 {
				return out
			}
			return nil
		}
		raw, err := cmd.Flags().GetString(name)
		if err != nil {
			return nil
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, item := range parts {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func newTunnelPropertiesFromFlags(cmd *cobra.Command) (*rstream.TunnelProperties, error) {
	namePtr := getStringPtr(cmd, "name")
	bytestreamPtr := getBoolPtr(cmd, "bytestream")
	datagramPtr := getBoolPtr(cmd, "datagram")
	var typePtr *rstream.TunnelType
	if bytestreamPtr != nil && *bytestreamPtr {
		t := rstream.TunnelTypeBytestream
		typePtr = &t
	} else if datagramPtr != nil && *datagramPtr {
		t := rstream.TunnelTypeDatagram
		typePtr = &t
	}
	publishPtr := getBoolPtr(cmd, "publish")
	noPublishPtr := getBoolPtr(cmd, "no-publish")
	var publishFinalPtr *bool
	switch {
	case publishPtr != nil && *publishPtr:
		publishFinalPtr = rstream.BoolPtr(true)
	case noPublishPtr != nil && *noPublishPtr:
		publishFinalPtr = rstream.BoolPtr(false)
	default:
	}
	tlsPtr := getBoolPtr(cmd, "tls")
	dtlsPtr := getBoolPtr(cmd, "dtls")
	quicPtr := getBoolPtr(cmd, "quic")
	httpPtr := getBoolPtr(cmd, "http")
	var protocol *rstream.Protocol
	if tlsPtr != nil && *tlsPtr {
		p := rstream.ProtocolTLS
		protocol = &p
	} else if dtlsPtr != nil && *dtlsPtr {
		p := rstream.ProtocolDTLS
		protocol = &p
	} else if quicPtr != nil && *quicPtr {
		p := rstream.ProtocolQUIC
		protocol = &p
	} else if httpPtr != nil && *httpPtr {
		p := rstream.ProtocolHTTP
		protocol = &p
	}
	labels := getStringArrayMap(cmd, "label")
	geoipSlice := getStringSlice(cmd, "geoip")
	trustedIPsSlice := getStringSlice(cmd, "trusted-ips")
	hostnamePtr := getStringPtr(cmd, "host")
	var tlsModePtr *rstream.TLSMode
	if cmd.Flags().Lookup("tls-mode").Changed {
		val, _ := cmd.Flags().GetString("tls-mode")
		tlsMode, err := parseForwardTLSMode(val)
		if err != nil {
			return nil, err
		}
		tlsModePtr = &tlsMode
	}
	var tlsALPNSlice []string
	if cmd.Flags().Lookup("tls-alpn").Changed {
		v, _ := cmd.Flags().GetString("tls-alpn")
		if v != "" {
			tlsALPNSlice = strings.Split(v, ",")
		}
		if len(tlsALPNSlice) == 0 {
			tlsALPNSlice = nil
		}
	}
	tlsMinVersionPtr := getStringPtr(cmd, "tls-min-version")
	tlsCipherIDs := getStringSlice(cmd, "tls-ciphers")
	mtlsPtr := getBoolPtr(cmd, "mtls")
	var httpVersionPtr *rstream.HTTPVersion
	if cmd.Flags().Lookup("http-version").Changed {
		val, _ := cmd.Flags().GetString("http-version")
		httpVersion, err := parseForwardHTTPVersion(val)
		if err != nil {
			return nil, err
		}
		httpVersionPtr = &httpVersion
	}
	httpUseTLSPtr := getBoolPtr(cmd, "http-use-tls")
	upstreamTLSPtr := getBoolPtr(cmd, "upstream-tls")
	if upstreamTLSPtr == nil {
		upstreamTLSPtr = httpUseTLSPtr
	}
	if httpUseTLSPtr == nil && upstreamTLSPtr != nil && (protocol == nil || *protocol == rstream.ProtocolHTTP) {
		httpUseTLSPtr = upstreamTLSPtr
	}
	tokenAuthPtr := getBoolPtr(cmd, "token-auth")
	rstreamAuthPtr := getBoolPtr(cmd, "rstream-auth")
	challengeModePtr := getBoolPtr(cmd, "challenge-mode")
	if protocol != nil && *protocol != rstream.ProtocolHTTP {
		if tokenAuthPtr != nil || rstreamAuthPtr != nil || challengeModePtr != nil {
			return nil, fmt.Errorf("--token-auth, --rstream-auth and --challenge-mode require --http")
		}
	}
	tunnelProperties := &rstream.TunnelProperties{
		Name:          namePtr,
		Type:          typePtr,
		Publish:       publishFinalPtr,
		Protocol:      protocol,
		Labels:        labels,
		GeoIP:         geoipSlice,
		TrustedIPs:    trustedIPsSlice,
		Hostname:      hostnamePtr,
		TLSMode:       tlsModePtr,
		TLSALPNs:      tlsALPNSlice,
		TLSMinVersion: tlsMinVersionPtr,
		TLSCiphers:    tlsCipherIDs,
		MTLSAuth:      mtlsPtr,
		HTTPVersion:   httpVersionPtr,
		HTTPUseTLS:    httpUseTLSPtr,
		UpstreamTLS:   upstreamTLSPtr,
		TokenAuth:     tokenAuthPtr,
		RstreamAuth:   rstreamAuthPtr,
		ChallengeMode: challengeModePtr,
	}
	return tunnelProperties, nil
}

func parseForwardTLSMode(value string) (rstream.TLSMode, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case string(rstream.TLSModeTerminated):
		return rstream.TLSModeTerminated, nil
	case string(rstream.TLSModePassthrough):
		return rstream.TLSModePassthrough, nil
	default:
		return "", fmt.Errorf("invalid --tls-mode %q (valid: terminated, passthrough)", value)
	}
}

func parseForwardHTTPVersion(value string) (rstream.HTTPVersion, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case string(rstream.HTTP1_1):
		return rstream.HTTP1_1, nil
	case "h2c":
		return rstream.HTTP2, nil
	case string(rstream.HTTP3):
		return rstream.HTTP3, nil
	default:
		return "", fmt.Errorf("invalid --http-version %q (valid: http/1.1, h2c, h3)", value)
	}
}
