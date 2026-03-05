// See LICENSE file in the project root for license information.

package cmd

import (
	"fmt"
	"strconv"
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
	hostPtr := getStringPtr(cmd, "host")
	var tlsModePtr *rstream.TLSMode
	if cmd.Flags().Lookup("tls-mode").Changed {
		val, _ := cmd.Flags().GetString("tls-mode")
		switch val {
		case "terminated":
			tmp := rstream.TLSModeTerminated
			tlsModePtr = &tmp
		case "passthrough":
			tmp := rstream.TLSModePassthrough
			tlsModePtr = &tmp
		}
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
	mtlsCACertPtr := getStringPtr(cmd, "mtls-cacert-file")
	var httpVersionPtr *rstream.HTTPVersion
	if cmd.Flags().Lookup("http-version").Changed {
		val, _ := cmd.Flags().GetString("http-version")
		switch val {
		case string(rstream.HTTP1_1):
			tmp := rstream.HTTP1_1
			httpVersionPtr = &tmp
		case "h2c":
			tmp := rstream.HTTP2
			httpVersionPtr = &tmp
		}
	}
	httpUseTLSPtr := getBoolPtr(cmd, "http-use-tls")
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
		Host:          hostPtr,
		TLSMode:       tlsModePtr,
		TLSALPNs:      tlsALPNSlice,
		TLSMinVersion: tlsMinVersionPtr,
		TLSCiphers:    tlsCipherIDs,
		MTLS:          mtlsPtr,
		MTLSCACertPEM: mtlsCACertPtr,
		HTTPVersion:   httpVersionPtr,
		HTTPUseTLS:    httpUseTLSPtr,
		TokenAuth:     tokenAuthPtr,
		RstreamAuth:   rstreamAuthPtr,
		ChallengeMode: challengeModePtr,
	}
	return tunnelProperties, nil
}

func parseTLSCipher(cipherName string) (uint16, error) {
	if strings.HasPrefix(cipherName, "0x") {
		val, err := strconv.ParseUint(strings.TrimPrefix(cipherName, "0x"), 16, 16)
		if err != nil {
			return 0, err
		}
		return uint16(val), nil
	}
	val, err := strconv.ParseUint(cipherName, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("unable to parse cipher: %q", cipherName)
	}
	return uint16(val), nil
}
