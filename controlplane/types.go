// See LICENSE file in the project root for license information.

package controlplane

import "encoding/json"

type Whoami struct {
	ID          string   `json:"id"`
	Role        string   `json:"role"`
	Name        string   `json:"name,omitempty"`
	Email       string   `json:"email,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

type Project struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	// Deprecated: use 'Domain' and 'EnginePort'.
	URL        string `json:"url"`
	Domain     string `json:"domain,omitempty"`
	EnginePort int    `json:"enginePort,omitempty"`
	Status     string `json:"status"`
	Provider   string `json:"provider"`
	Region     string `json:"region,omitempty"`
	Plan       string `json:"plan"`
	Deployment string `json:"deployment"`
	TurnDomain string `json:"turnDomain,omitempty"`
	TurnPort   int    `json:"turnPort,omitempty"`
	TurnsPort  int    `json:"turnsPort,omitempty"`
}

type Workspace struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	Name               string `json:"name"`
	SubscriptionStatus string `json:"subscriptionStatus,omitempty"`
	HasPaymentMethod   bool   `json:"hasPaymentMethod,omitempty"`
	HasBasicTier       bool   `json:"hasBasicTier,omitempty"`
	Membership         any    `json:"membership,omitempty"`
}

type ListWorkspacesResponse struct {
	Workspaces []Workspace `json:"workspaces"`
}

type ProjectCreationBillingImpact struct {
	Type           string `json:"type"`
	Label          string `json:"label"`
	Description    string `json:"description"`
	BillingEnabled bool   `json:"billingEnabled"`
	Price          any    `json:"price,omitempty"`
	CompareAtPrice any    `json:"compareAtPrice,omitempty"`
	Recurring      string `json:"recurring,omitempty"`
	Promotion      any    `json:"promotion,omitempty"`
}

type ProjectCreationBillingAction struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
	SubmitLabel string `json:"submitLabel"`
}

type ProjectCreationRegion struct {
	Region      string `json:"region"`
	Recommended bool   `json:"recommended"`
}

type ProjectCreationProvider struct {
	Provider string                  `json:"provider"`
	Regions  []ProjectCreationRegion `json:"regions"`
}

type ProjectCreationOption struct {
	Plan                  string                       `json:"plan"`
	Name                  string                       `json:"name"`
	Description           string                       `json:"description"`
	Available             bool                         `json:"available"`
	UnavailableReasonCode string                       `json:"unavailableReasonCode,omitempty"`
	UnavailableReason     string                       `json:"unavailableReason,omitempty"`
	BillingImpact         ProjectCreationBillingImpact `json:"billingImpact"`
	BillingAction         ProjectCreationBillingAction `json:"billingAction"`
	Providers             []ProjectCreationProvider    `json:"providers"`
	CreationFingerprint   string                       `json:"creationFingerprint"`
}

type RecommendedProjectCreationOption struct {
	Plan     string `json:"plan"`
	Provider string `json:"provider"`
	Region   string `json:"region"`
}

type ProjectCreationOptionsResponse struct {
	Plans       []ProjectCreationOption           `json:"plans"`
	Recommended *RecommendedProjectCreationOption `json:"recommended,omitempty"`
}

type ProjectPlan map[string]any

type CreateProjectRequest struct {
	Name                string `json:"name"`
	Provider            string `json:"provider"`
	Region              string `json:"region"`
	Plan                string `json:"plan"`
	CreationFingerprint string `json:"creationFingerprint"`
	IdempotencyKey      string `json:"idempotencyKey"`
}

type CreateProjectCheckoutResponse struct {
	URL       string `json:"url"`
	ProjectID string `json:"projectId"`
}

type TURNCredentials struct {
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
	URLs       []string `json:"urls"`
	TTL        int      `json:"ttl"`
}

type CreateTURNCredentialsRequest struct {
	TTLSeconds *int `json:"ttlSeconds,omitempty"`
}

type ListProjectsResponse struct {
	Projects   []Project `json:"projects"`
	Page       int       `json:"page"`
	PageSize   int       `json:"pageSize"`
	Total      int       `json:"total"`
	TotalPages int       `json:"totalPages"`
}

type ListProjectsParams struct {
	Query    string
	Page     *int
	PageSize *int
	Sort     string
	Order    string
}

type CreateTokenRequest struct {
	Permissions *[]string        `json:"permissions"`
	Resources   *json.RawMessage `json:"resources,omitempty"`
}

type CreateTokenResponse struct {
	Token string `json:"token"`
}

type ProjectLogsParams struct {
	Timeline     string
	Start        string
	End          string
	EventType    string
	AfterEventID string
	Page         *int
	PageSize     *int
	Order        string
}

type ProjectLogEvent map[string]any

type ProjectLogsResponse struct {
	Events     []ProjectLogEvent `json:"events"`
	Page       int               `json:"page"`
	PageSize   int               `json:"pageSize"`
	Total      int               `json:"total"`
	TotalPages int               `json:"totalPages"`
}

type ProjectSettings map[string]any

type ProjectUsage map[string]any

type ProjectTURNUsage map[string]any

type ProjectDomain map[string]any

type ListProjectDomainsParams struct {
	Query    string
	Page     *int
	PageSize *int
	Sort     string
	Order    string
}

type ListProjectDomainsResponse struct {
	Domains    []ProjectDomain `json:"domains"`
	Page       int             `json:"page"`
	PageSize   int             `json:"pageSize"`
	Total      int             `json:"total"`
	TotalPages int             `json:"totalPages"`
}

type CreateProjectDomainRequest struct {
	Hostname string `json:"hostname"`
}

type DomainConnectResponse map[string]any

type WorkspaceMembersParams struct {
	Query    string
	Page     *int
	PageSize *int
	Sort     string
	Order    string
}

type WorkspaceMember struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	InvitedAt string `json:"invitedAt,omitempty"`
	JoinedAt  string `json:"joinedAt,omitempty"`
}

type WorkspaceMembersResponse struct {
	Members    []WorkspaceMember `json:"members"`
	Page       int               `json:"page"`
	PageSize   int               `json:"pageSize"`
	Total      int               `json:"total"`
	TotalPages int               `json:"totalPages"`
}
