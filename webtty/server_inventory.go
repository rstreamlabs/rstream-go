// See LICENSE file in the project root for license information.

package webtty

import (
	"strings"

	rstream "github.com/rstreamlabs/rstream-go"
)

type ServerInfo struct {
	Status            string            `json:"status"`
	TunnelID          string            `json:"tunnel_id"`
	TunnelName        *string           `json:"tunnel_name,omitempty"`
	Target            string            `json:"target"`
	RstreamURL        string            `json:"rstream_url"`
	Publish           bool              `json:"publish"`
	Host              *string           `json:"host,omitempty"`
	TokenAuth         bool              `json:"token_auth"`
	OSFamily          *string           `json:"os_family,omitempty"`
	Arch              *string           `json:"arch,omitempty"`
	OSID              *string           `json:"os_id,omitempty"`
	OSVersionID       *string           `json:"os_version_id,omitempty"`
	OSVersionCodename *string           `json:"os_version_codename,omitempty"`
	OSPrettyName      *string           `json:"os_pretty_name,omitempty"`
	KernelRelease     *string           `json:"kernel_release,omitempty"`
	Hostname          *string           `json:"hostname,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
}

func ParseServers(tunnels []rstream.TunnelInventory) []ServerInfo {
	out := make([]ServerInfo, 0, len(tunnels))
	for _, tunnel := range tunnels {
		server, ok := parseServer(tunnel)
		if !ok {
			continue
		}
		out = append(out, server)
	}
	return out
}

func parseServer(tunnel rstream.TunnelInventory) (ServerInfo, bool) {
	labels := tunnel.Labels
	if labels[webTTYApplicationProtocolKey] != WebTTYApplicationProtocol {
		return ServerInfo{}, false
	}
	id := trimStringPtr(tunnel.ID)
	if id == "" {
		return ServerInfo{}, false
	}
	if tunnel.Publish != nil && *tunnel.Publish {
		if tunnel.Protocol != nil && *tunnel.Protocol != rstream.ProtocolHTTP {
			return ServerInfo{}, false
		}
	}
	target := id
	name := trimStringPtr(tunnel.Name)
	if name != "" {
		target = name
	}
	info := ServerInfo{
		Status:            strings.TrimSpace(tunnel.Status),
		TunnelID:          id,
		TunnelName:        cloneStringPtr(name),
		Target:            target,
		RstreamURL:        "rstrm://" + target,
		Publish:           tunnel.Publish != nil && *tunnel.Publish,
		Host:              cloneStringPtr(trimStringPtr(tunnel.Host)),
		TokenAuth:         tunnel.TokenAuth != nil && *tunnel.TokenAuth,
		OSFamily:          cloneStringPtr(labels[webTTYOSFamilyLabel]),
		Arch:              cloneStringPtr(labels[webTTYArchLabel]),
		OSID:              cloneStringPtr(labels[webTTYOSIDLabel]),
		OSVersionID:       cloneStringPtr(labels[webTTYOSVersionIDLabel]),
		OSVersionCodename: cloneStringPtr(labels[webTTYOSVersionCodenameLabel]),
		OSPrettyName:      cloneStringPtr(labels[webTTYOSPrettyNameLabel]),
		KernelRelease:     cloneStringPtr(labels[webTTYKernelReleaseLabel]),
		Hostname:          cloneStringPtr(labels[webTTYHostnameLabel]),
	}
	customLabels := make(map[string]string)
	for key, value := range labels {
		if !strings.HasPrefix(key, webTTYLabelPrefix) {
			continue
		}
		labelKey := strings.TrimSpace(strings.TrimPrefix(key, webTTYLabelPrefix))
		if labelKey == "" {
			continue
		}
		customLabels[labelKey] = value
	}
	if len(customLabels) > 0 {
		info.Labels = customLabels
	}
	return info, true
}

func trimStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func cloneStringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	return &value
}
