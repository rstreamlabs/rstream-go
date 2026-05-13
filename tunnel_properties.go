// See LICENSE file in the project root for license information.

package rstream

import "time"

type TunnelType string

const (
	TunnelTypeBytestream TunnelType = "bytestream"
	TunnelTypeDatagram   TunnelType = "datagram"
)

type Protocol string

const (
	ProtocolTLS  Protocol = "tls"  // bytestream
	ProtocolDTLS Protocol = "dtls" // datagram
	ProtocolQUIC Protocol = "quic" // datagram
	ProtocolHTTP Protocol = "http" // bytestream (HTTP/1.1, HTTP/2) or datagram (HTTP/3)
)

type TLSMode string

const (
	TLSModePassthrough TLSMode = "passthrough" // For TLS tunnels only
	TLSModeTerminated  TLSMode = "terminated"
)

type HTTPVersion string

const (
	HTTP1_1 HTTPVersion = "http/1.1" // HTTP/1.1 (cleartext)
	HTTP2   HTTPVersion = "h2c"      // HTTP/2 (cleartext)
	HTTP3   HTTPVersion = "h3"       // HTTP/3
)

type TunnelProperties struct {
	ID            *string           `json:"id,omitempty"`
	CreationDate  *time.Time        `json:"creation_date,omitempty"`
	Name          *string           `json:"name,omitempty"`
	Type          *TunnelType       `json:"type,omitempty"`
	Publish       *bool             `json:"publish,omitempty"`
	Protocol      *Protocol         `json:"protocol,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	GeoIP         []string          `json:"geo_ip,omitempty"`
	TrustedIPs    []string          `json:"trusted_ips,omitempty"`
	Host          *string           `json:"host,omitempty"`
	Hostname      *string           `json:"hostname,omitempty"`
	Port          *uint32           `json:"port,omitempty"`
	TLSMode       *TLSMode          `json:"tls_mode,omitempty"`
	TLSALPNs      []string          `json:"tls_alpns,omitempty"`
	TLSMinVersion *string           `json:"tls_min_version,omitempty"`
	TLSCiphers    []string          `json:"tls_ciphers,omitempty"`
	MTLSAuth      *bool             `json:"mtls_auth,omitempty"`
	HTTPVersion   *HTTPVersion      `json:"http_version,omitempty"`
	HTTPUseTLS    *bool             `json:"http_use_tls,omitempty"`
	UpstreamTLS   *bool             `json:"upstream_tls,omitempty"`
	TokenAuth     *bool             `json:"token_auth,omitempty"`
	RstreamAuth   *bool             `json:"rstream_auth,omitempty"`
	ChallengeMode *bool             `json:"challenge_mode,omitempty"`
}

type TunnelInventory struct {
	TunnelProperties
	Status   string `json:"status"`
	ClientID string `json:"client_id,omitempty"`
}

type ListTunnelsFilters struct {
	ID          *string            `json:"id,omitempty"`
	Name        *string            `json:"name,omitempty"`
	Type        *string            `json:"type,omitempty"`
	Status      *string            `json:"status,omitempty"`
	ClientID    *string            `json:"client_id,omitempty"`
	UserID      *string            `json:"user_id,omitempty"`
	Protocol    *string            `json:"protocol,omitempty"`
	Hostname    *string            `json:"hostname,omitempty"`
	Publish     *bool              `json:"publish,omitempty"`
	HTTPVersion *string            `json:"http_version,omitempty"`
	Labels      map[string]*string `json:"labels,omitempty"`
}

type ListTunnelsParams struct {
	Limit   *int                `json:"limit,omitempty"`
	Filters *ListTunnelsFilters `json:"filters,omitempty"`
}

type ListTunnelsResponse = []TunnelInventory
