// See LICENSE file in the project root for license information.

package rstream

import (
	"errors"
	"time"
)

type TunnelType string

const (
	TunnelTypeBytestream TunnelType = "bytestream"
	TunnelTypeDatagram   TunnelType = "datagram"
)

type Protocol string

const (
	ProtocolTLS    Protocol = "tls"    // bytestream
	ProtocolTCP    Protocol = "tcp"    // bytestream
	ProtocolDTLS   Protocol = "dtls"   // datagram
	ProtocolQUIC   Protocol = "quic"   // datagram
	ProtocolHTTP   Protocol = "http"   // bytestream (HTTP/1.1, HTTP/2) or datagram (HTTP/3)
	ProtocolWebTTY Protocol = "webtty" // managed WebTTY envelope
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
	ID                         *string           `json:"id,omitempty"`
	CreationDate               *time.Time        `json:"creation_date,omitempty"`
	Name                       *string           `json:"name,omitempty"`
	Type                       *TunnelType       `json:"type,omitempty"`
	Publish                    *bool             `json:"publish,omitempty"`
	Protocol                   *Protocol         `json:"protocol,omitempty"`
	Labels                     map[string]string `json:"labels,omitempty"`
	GeoIP                      []string          `json:"geo_ip,omitempty"`
	TrustedIPs                 []string          `json:"trusted_ips,omitempty"`
	Host                       *string           `json:"host,omitempty"`
	Hostname                   *string           `json:"hostname,omitempty"`
	Port                       *uint32           `json:"port,omitempty"`
	TLSMode                    *TLSMode          `json:"tls_mode,omitempty"`
	TLSALPNs                   []string          `json:"tls_alpns,omitempty"`
	TLSMinVersion              *string           `json:"tls_min_version,omitempty"`
	TLSCiphers                 []string          `json:"tls_ciphers,omitempty"`
	MTLSAuth                   *bool             `json:"mtls_auth,omitempty"`
	HTTPVersion                *HTTPVersion      `json:"http_version,omitempty"`
	HTTPUseTLS                 *bool             `json:"http_use_tls,omitempty"`
	UpstreamTLS                *bool             `json:"upstream_tls,omitempty"`
	DatagramGuaranteedDelivery *bool             `json:"datagram_guaranteed_delivery,omitempty"`
	AllowCrossRegionRouting    *bool             `json:"allow_cross_region_routing,omitempty"`
	TokenAuth                  *bool             `json:"token_auth,omitempty"`
	RstreamAuth                *bool             `json:"rstream_auth,omitempty"`
	ChallengeMode              *bool             `json:"challenge_mode,omitempty"`
}

type TunnelInventory struct {
	TunnelProperties
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Status      string `json:"status"`
	ClientID    string `json:"client_id,omitempty"`
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

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTunnelProperties(props TunnelProperties) TunnelProperties {
	return TunnelProperties{
		ID:                         clonePtr(props.ID),
		CreationDate:               clonePtr(props.CreationDate),
		Name:                       clonePtr(props.Name),
		Type:                       clonePtr(props.Type),
		Publish:                    clonePtr(props.Publish),
		Protocol:                   clonePtr(props.Protocol),
		Labels:                     cloneStringMap(props.Labels),
		GeoIP:                      append([]string(nil), props.GeoIP...),
		TrustedIPs:                 append([]string(nil), props.TrustedIPs...),
		Host:                       clonePtr(props.Host),
		Hostname:                   clonePtr(props.Hostname),
		Port:                       clonePtr(props.Port),
		TLSMode:                    clonePtr(props.TLSMode),
		TLSALPNs:                   append([]string(nil), props.TLSALPNs...),
		TLSMinVersion:              clonePtr(props.TLSMinVersion),
		TLSCiphers:                 append([]string(nil), props.TLSCiphers...),
		MTLSAuth:                   clonePtr(props.MTLSAuth),
		HTTPVersion:                clonePtr(props.HTTPVersion),
		HTTPUseTLS:                 clonePtr(props.HTTPUseTLS),
		UpstreamTLS:                clonePtr(props.UpstreamTLS),
		DatagramGuaranteedDelivery: clonePtr(props.DatagramGuaranteedDelivery),
		AllowCrossRegionRouting:    clonePtr(props.AllowCrossRegionRouting),
		TokenAuth:                  clonePtr(props.TokenAuth),
		RstreamAuth:                clonePtr(props.RstreamAuth),
		ChallengeMode:              clonePtr(props.ChallengeMode),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func normalizeCreateTunnelProperties(props TunnelProperties) (TunnelProperties, error) {
	if props.Port != nil && (props.Protocol == nil || *props.Protocol != ProtocolTCP) {
		return props, errors.New("a published port requires protocol tcp")
	}
	if props.Protocol == nil || *props.Protocol != ProtocolTCP {
		return props, nil
	}
	if props.Port != nil && (*props.Port == 0 || *props.Port > 65535) {
		return props, errors.New("published TCP port must be between 1 and 65535")
	}
	if props.Type != nil && *props.Type != TunnelTypeBytestream {
		return props, errors.New("published TCP tunnels require a bytestream tunnel")
	}
	if props.Publish != nil && !*props.Publish {
		return props, errors.New("published TCP tunnels cannot be unpublished")
	}
	if props.Hostname != nil || props.TLSMode != nil || len(props.TLSALPNs) > 0 || props.TLSMinVersion != nil || len(props.TLSCiphers) > 0 || props.MTLSAuth != nil || props.HTTPVersion != nil || props.HTTPUseTLS != nil || props.UpstreamTLS != nil || props.DatagramGuaranteedDelivery != nil || props.TokenAuth != nil || props.RstreamAuth != nil || props.ChallengeMode != nil {
		return props, errors.New("published TCP tunnels do not accept hostname, HTTP, TLS, edge authentication, or datagram delivery options")
	}
	props.Type = TunnelTypePtr(TunnelTypeBytestream)
	props.Publish = BoolPtr(true)
	return props, nil
}
