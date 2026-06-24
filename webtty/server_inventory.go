// See LICENSE file in the project root for license information.

package webtty

import (
	"net"
	"strconv"
	"strings"

	rstream "github.com/rstreamlabs/rstream-go"
)

type ServerInfo struct {
	Status            string            `json:"status"`
	TunnelID          string            `json:"tunnel_id"`
	TunnelProtocol    string            `json:"tunnel_protocol,omitempty"`
	Managed           bool              `json:"managed"`
	TunnelName        *string           `json:"tunnel_name,omitempty"`
	Target            string            `json:"target"`
	RstreamURL        string            `json:"rstream_url"`
	Publish           bool              `json:"publish"`
	Host              *string           `json:"host,omitempty"`
	TokenAuth         bool              `json:"token_auth"`
	ServerID          *string           `json:"server_id,omitempty"`
	ServerName        *string           `json:"server_name,omitempty"`
	WorkspaceID       *string           `json:"workspace_id,omitempty"`
	ProjectID         *string           `json:"project_id,omitempty"`
	HostKeyID         *string           `json:"host_key_id,omitempty"`
	E2E               *string           `json:"e2e,omitempty"`
	ClientProof       *string           `json:"client_proof,omitempty"`
	EncryptionPolicy  *string           `json:"encryption_policy,omitempty"`
	Capabilities      []string          `json:"capabilities,omitempty"`
	ExecPath          *string           `json:"exec_path,omitempty"`
	FSPath            *string           `json:"fs_path,omitempty"`
	FSMode            *string           `json:"fs_mode,omitempty"`
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
	managedProtocol := tunnel.Protocol != nil && *tunnel.Protocol == rstream.ProtocolWebTTY
	if !managedProtocol && labels[webTTYApplicationProtocolKey] != WebTTYApplicationProtocol {
		return ServerInfo{}, false
	}
	id := trimStringPtr(tunnel.ID)
	if id == "" {
		return ServerInfo{}, false
	}
	if tunnel.Publish != nil && *tunnel.Publish {
		if tunnel.Protocol != nil && *tunnel.Protocol != rstream.ProtocolHTTP && !managedProtocol {
			return ServerInfo{}, false
		}
	}
	name := trimStringPtr(tunnel.Name)
	serverID := strings.TrimSpace(labels[webTTYServerIDLabel])
	serverName := strings.TrimSpace(labels[webTTYServerNameLabel])
	target := firstNonEmpty(serverName, name, id)
	rstreamTarget := firstNonEmpty(serverID, name, id)
	host := trimStringPtr(tunnel.Hostname)
	if host != "" && tunnel.Port != nil && *tunnel.Port != 443 {
		host = net.JoinHostPort(host, strconv.FormatUint(uint64(*tunnel.Port), 10))
	}
	if host == "" {
		host = trimStringPtr(tunnel.Host)
	}
	info := ServerInfo{
		Status:            strings.TrimSpace(tunnel.Status),
		TunnelID:          id,
		TunnelProtocol:    tunnelProtocolString(tunnel.Protocol),
		Managed:           managedProtocol,
		TunnelName:        cloneStringPtr(name),
		Target:            target,
		RstreamURL:        "rstrm://" + rstreamTarget,
		Publish:           tunnel.Publish != nil && *tunnel.Publish,
		Host:              cloneStringPtr(host),
		TokenAuth:         tunnel.TokenAuth != nil && *tunnel.TokenAuth,
		ServerID:          cloneStringPtr(serverID),
		ServerName:        cloneStringPtr(serverName),
		WorkspaceID:       cloneStringPtr(tunnel.WorkspaceID),
		ProjectID:         cloneStringPtr(tunnel.ProjectID),
		HostKeyID:         cloneStringPtr(labels[webTTYHostKeyIDLabel]),
		E2E:               cloneStringPtr(labels[webTTYE2ELabel]),
		ClientProof:       cloneStringPtr(labels[webTTYClientProofLabel]),
		EncryptionPolicy:  cloneStringPtr(labels[webTTYEncryptionPolicyLabel]),
		Capabilities:      parseWebTTYCapabilities(labels[webTTYCapabilitiesLabel]),
		OSFamily:          cloneStringPtr(labels[webTTYOSFamilyLabel]),
		Arch:              cloneStringPtr(labels[webTTYArchLabel]),
		OSID:              cloneStringPtr(labels[webTTYOSIDLabel]),
		OSVersionID:       cloneStringPtr(labels[webTTYOSVersionIDLabel]),
		OSVersionCodename: cloneStringPtr(labels[webTTYOSVersionCodenameLabel]),
		OSPrettyName:      cloneStringPtr(labels[webTTYOSPrettyNameLabel]),
		KernelRelease:     cloneStringPtr(labels[webTTYKernelReleaseLabel]),
		Hostname:          cloneStringPtr(labels[webTTYHostnameLabel]),
	}
	if len(info.Capabilities) == 0 {
		info.Capabilities = []string{WebTTYCapabilityExec}
	}
	if serverHasCapability(info.Capabilities, WebTTYCapabilityExec) {
		info.ExecPath = cloneStringPtr(firstNonEmpty(labels[webTTYExecPathLabel], WebTTYDefaultExecPath))
	}
	if serverHasCapability(info.Capabilities, WebTTYCapabilityFS) {
		info.FSPath = cloneStringPtr(firstNonEmpty(labels[webTTYFSPathLabel], WebTTYDefaultFSPath))
		info.FSMode = cloneStringPtr(firstNonEmpty(labels[webTTYFSModeLabel], WebTTYDefaultFSMode))
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

func parseWebTTYCapabilities(raw string) []string {
	parts := strings.Split(raw, ",")
	values := map[string]struct{}{}
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		values[value] = struct{}{}
	}
	known := []string{WebTTYCapabilityExec, WebTTYCapabilityFS}
	out := make([]string, 0, len(known))
	for _, value := range known {
		if _, ok := values[value]; !ok {
			continue
		}
		out = append(out, value)
	}
	return out
}

func serverHasCapability(list []string, capability string) bool {
	for _, item := range list {
		if item == capability {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func tunnelProtocolString(value *rstream.Protocol) string {
	if value == nil {
		return ""
	}
	return string(*value)
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
