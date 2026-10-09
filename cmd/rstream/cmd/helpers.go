// See LICENSE file in the project root for license information.

package cmd

import (
	"strings"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/tunnelconfig"
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

func getUint32Ptr(cmd *cobra.Command, name string) *uint32 {
	f := cmd.Flags().Lookup(name)
	if f != nil && f.Changed {
		val, _ := cmd.Flags().GetUint32(name)
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
	publish, err := tunnelconfig.Bool(getBoolPtr(cmd, "publish"), getBoolPtr(cmd, "no-publish"))
	if err != nil {
		return nil, err
	}
	props := rstream.TunnelProperties{
		Name:                       getStringPtr(cmd, "name"),
		Publish:                    publish,
		Labels:                     getStringArrayMap(cmd, "label"),
		GeoIP:                      getStringSlice(cmd, "geoip"),
		TrustedIPs:                 getStringSlice(cmd, "trusted-ips"),
		Hostname:                   getStringPtr(cmd, "host"),
		Port:                       getUint32Ptr(cmd, "tcp-port"),
		AllowCrossRegionRouting:    getBoolPtr(cmd, "allow-cross-region-routing"),
		TLSMode:                    stringOption[rstream.TLSMode](getStringPtr(cmd, "tls-mode")),
		TLSALPNs:                   getStringSlice(cmd, "tls-alpn"),
		TLSMinVersion:              getStringPtr(cmd, "tls-min-version"),
		TLSCiphers:                 getStringSlice(cmd, "tls-ciphers"),
		MTLSAuth:                   getBoolPtr(cmd, "mtls"),
		HTTPVersion:                stringOption[rstream.HTTPVersion](getStringPtr(cmd, "http-version")),
		UpstreamTLS:                getBoolPtr(cmd, "upstream-tls"),
		DatagramGuaranteedDelivery: getBoolPtr(cmd, "datagram-guaranteed-delivery"),
		TokenAuth:                  getBoolPtr(cmd, "token-auth"),
		RstreamAuth:                getBoolPtr(cmd, "rstream-auth"),
		ChallengeMode:              getBoolPtr(cmd, "challenge-mode"),
	}
	for _, name := range []string{"bytestream", "datagram"} {
		if value := getBoolPtr(cmd, name); value != nil && *value {
			props.Type = rstream.TunnelTypePtr(rstream.TunnelType(name))
		}
	}
	for _, name := range []string{"http", "tls", "tcp", "dtls", "quic"} {
		if value := getBoolPtr(cmd, name); value != nil && *value {
			props.Protocol = rstream.ProtocolPtr(rstream.Protocol(name))
		}
	}
	resolved, err := tunnelconfig.Resolve(props)
	if err != nil {
		return nil, err
	}
	return &resolved, nil
}

func stringOption[T ~string](value *string) *T {
	if value == nil {
		return nil
	}
	result := T(*value)
	return &result
}

func parseForwardTLSMode(value string) (rstream.TLSMode, error) {
	return tunnelconfig.ParseTLSMode(value)
}

func parseForwardHTTPVersion(value string) (rstream.HTTPVersion, error) {
	return tunnelconfig.ParseHTTPVersion(value)
}
