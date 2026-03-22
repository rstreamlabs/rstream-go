// See LICENSE file in the project root for license information.

package rstream

type ClientProperties struct {
	ID              string            `json:"id"`
	Status          string            `json:"status"`
	UserID          *string           `json:"user_id,omitempty"`
	WorkspaceID     *string           `json:"workspace_id,omitempty"`
	ProjectID       *string           `json:"project_id,omitempty"`
	ClusterID       *string           `json:"cluster_id,omitempty"`
	Plan            *string           `json:"plan,omitempty"`
	Provider        *string           `json:"provider,omitempty"`
	Region          *string           `json:"region,omitempty"`
	Agent           *string           `json:"agent,omitempty"`
	Channel         *string           `json:"channel,omitempty"`
	Version         *string           `json:"version,omitempty"`
	OS              *string           `json:"os,omitempty"`
	Arch            *string           `json:"arch,omitempty"`
	ProtocolVersion *string           `json:"protocol_version,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type ListClientsFilters struct {
	ID              *string            `json:"id,omitempty"`
	Status          *string            `json:"status,omitempty"`
	UserID          *string            `json:"user_id,omitempty"`
	Agent           *string            `json:"agent,omitempty"`
	Channel         *string            `json:"channel,omitempty"`
	Version         *string            `json:"version,omitempty"`
	OS              *string            `json:"os,omitempty"`
	Arch            *string            `json:"arch,omitempty"`
	ProtocolVersion *string            `json:"protocol_version,omitempty"`
	Labels          map[string]*string `json:"labels,omitempty"`
}

type ListClientsParams struct {
	Limit   *int                `json:"limit,omitempty"`
	Filters *ListClientsFilters `json:"filters,omitempty"`
}

type ListClientsResponse = []ClientProperties

type WatchParams struct {
	Clients *ListClientsFilters `json:"clients,omitempty"`
	Tunnels *ListTunnelsFilters `json:"tunnels,omitempty"`
}
