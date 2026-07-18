// See LICENSE file in the project root for license information.

package controlplane

import (
	"encoding/json"
	"fmt"
)

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
	ID                 string                      `json:"id"`
	Type               string                      `json:"type"`
	Name               string                      `json:"name"`
	SubscriptionStatus string                      `json:"subscriptionStatus,omitempty"`
	HasPaymentMethod   bool                        `json:"hasPaymentMethod,omitempty"`
	HasBasicTier       bool                        `json:"hasBasicTier,omitempty"`
	Enterprise         *WorkspaceEnterpriseSummary `json:"enterprise,omitempty"`
	Membership         *WorkspaceMembership        `json:"membership,omitempty"`
}

type ListWorkspacesResponse struct {
	Workspaces []Workspace `json:"workspaces"`
}

type WorkspaceMembership struct {
	Role      string `json:"role,omitempty"`
	Status    string `json:"status,omitempty"`
	InvitedAt string `json:"invitedAt,omitempty"`
	JoinedAt  string `json:"joinedAt,omitempty"`
}

type WorkspaceEnterpriseSummary struct {
	Status              string `json:"status,omitempty"`
	ProjectCreationMode string `json:"projectCreationMode,omitempty"`
	WorkspaceKeyMode    string `json:"workspaceKeyMode,omitempty"`
}

type ListWorkspacesParams struct {
	Type             string
	MembershipStatus string
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

type WebTTYServerCapabilities struct {
	Transports     []string          `json:"transports,omitempty"`
	ExecutionModes []string          `json:"execution_modes,omitempty"`
	Encryption     []string          `json:"encryption,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

type WebTTYServer struct {
	ID                            string                    `json:"id"`
	WorkspaceID                   string                    `json:"workspaceId"`
	ProjectID                     string                    `json:"projectId"`
	Name                          string                    `json:"name"`
	Description                   *string                   `json:"description,omitempty"`
	Status                        string                    `json:"status"`
	RecordingPolicy               string                    `json:"recordingPolicy"`
	EncryptionPolicy              string                    `json:"encryptionPolicy"`
	AccessPolicy                  string                    `json:"accessPolicy"`
	Labels                        map[string]string         `json:"labels,omitempty"`
	Capabilities                  *WebTTYServerCapabilities `json:"capabilities,omitempty"`
	ServerPublicKey               *string                   `json:"serverPublicKey,omitempty"`
	ServerSigningKeyID            *string                   `json:"serverSigningKeyId,omitempty"`
	ServerSigningPublicKey        *string                   `json:"serverSigningPublicKey,omitempty"`
	ServerFingerprint             *string                   `json:"serverFingerprint,omitempty"`
	ServerKeyAlgorithm            *string                   `json:"serverKeyAlgorithm,omitempty"`
	ServerKeyStatus               *string                   `json:"serverKeyStatus,omitempty"`
	WorkspaceTrustKeysetID        *string                   `json:"workspaceTrustKeysetId,omitempty"`
	WorkspaceTrustDeviceKeyID     *string                   `json:"workspaceTrustDeviceKeyId,omitempty"`
	WorkspaceTrustPayloadHash     *string                   `json:"workspaceTrustPayloadHash,omitempty"`
	WorkspaceTrustActorSignature  *string                   `json:"workspaceTrustActorSignature,omitempty"`
	WorkspaceTrustKeysetSignature *string                   `json:"workspaceTrustKeysetSignature,omitempty"`
	WorkspaceTrustSignedAt        *string                   `json:"workspaceTrustSignedAt,omitempty"`
	EnrolledAt                    *string                   `json:"enrolledAt,omitempty"`
	CreatedByUserID               *string                   `json:"createdByUserId,omitempty"`
	CreatedAt                     string                    `json:"createdAt"`
	UpdatedAt                     string                    `json:"updatedAt"`
}

type ListWebTTYServersParams struct {
	Query    string
	Status   string
	Page     *int
	PageSize *int
}

type ListWebTTYServersResponse struct {
	Servers    []WebTTYServer `json:"servers"`
	Page       int            `json:"page"`
	PageSize   int            `json:"pageSize"`
	Total      int            `json:"total"`
	TotalPages int            `json:"totalPages"`
}

type CreateWebTTYServerRequest struct {
	Name             string            `json:"name"`
	Description      *string           `json:"description,omitempty"`
	RecordingPolicy  string            `json:"recordingPolicy,omitempty"`
	EncryptionPolicy string            `json:"encryptionPolicy,omitempty"`
	AccessPolicy     string            `json:"accessPolicy,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
}

type UpdateWebTTYServerRequest struct {
	Name            *string           `json:"name,omitempty"`
	Description     *string           `json:"description,omitempty"`
	Status          *string           `json:"status,omitempty"`
	RecordingPolicy *string           `json:"recordingPolicy,omitempty"`
	AccessPolicy    *string           `json:"accessPolicy,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type CreateWebTTYServerResponse struct {
	Server WebTTYServer `json:"server"`
}

type EnrollWebTTYServerRequest struct {
	ServerPublicKey        string                    `json:"serverPublicKey"`
	ServerSigningKeyID     string                    `json:"serverSigningKeyId"`
	ServerSigningPublicKey string                    `json:"serverSigningPublicKey"`
	ServerFingerprint      string                    `json:"serverFingerprint,omitempty"`
	ServerKeyAlgorithm     string                    `json:"serverKeyAlgorithm"`
	Capabilities           *WebTTYServerCapabilities `json:"capabilities,omitempty"`
}

type ApproveWebTTYServerWorkspaceTrustRequest struct {
	ActorDeviceKeyID string `json:"actorDeviceKeyId"`
	KeysetID         string `json:"keysetId"`
	SignedAt         string `json:"signedAt"`
	ActorSignature   string `json:"actorSignature"`
	KeysetSignature  string `json:"keysetSignature"`
}

type ResolveWebTTYServerClientRequest struct {
	DeviceProofs []WorkspaceDeviceAccessProof `json:"deviceProofs"`
}

type ResolveWebTTYServerClientResponse struct {
	ServerID               string                         `json:"serverId"`
	WorkspaceID            string                         `json:"workspaceId"`
	ProjectID              string                         `json:"projectId"`
	EncryptionPolicy       string                         `json:"encryptionPolicy"`
	E2ERequired            bool                           `json:"e2eRequired"`
	ServerPublicKey        *string                        `json:"serverPublicKey"`
	ServerSigningKeyID     *string                        `json:"serverSigningKeyId"`
	ServerSigningPublicKey *string                        `json:"serverSigningPublicKey"`
	ServerEndpointIdentity *string                        `json:"serverEndpointIdentity"`
	ServerFingerprint      *string                        `json:"serverFingerprint"`
	ServerKeyAlgorithm     *string                        `json:"serverKeyAlgorithm"`
	ServerKeyStatus        *string                        `json:"serverKeyStatus"`
	CurrentDevice          *WebTTYCurrentDeviceResolution `json:"currentDevice,omitempty"`
}

type WebTTYCurrentDeviceResolution struct {
	DeviceKeyID          string          `json:"deviceKeyId"`
	Kind                 string          `json:"kind"`
	PublicEncryptionKey  string          `json:"publicEncryptionKey"`
	PublicSigningKey     string          `json:"publicSigningKey"`
	Fingerprint          string          `json:"fingerprint"`
	WebTTYPublicKey      string          `json:"webttyPublicKey"`
	WebTTYKeyID          string          `json:"webttyKeyId"`
	WebTTYKeyAlgorithm   string          `json:"webttyKeyAlgorithm"`
	TrustKeysetID        string          `json:"trustKeysetId"`
	TrustSource          string          `json:"trustSource"`
	TrustPayload         json.RawMessage `json:"trustPayload"`
	TrustPayloadHash     string          `json:"trustPayloadHash"`
	TrustActorSignature  *string         `json:"trustActorSignature"`
	TrustKeysetSignature string          `json:"trustKeysetSignature"`
	TrustSignedAt        string          `json:"trustSignedAt"`
}

type CreateWorkspaceDeviceKeyRequest struct {
	Kind                string `json:"kind"`
	Label               string `json:"label,omitempty"`
	BrowserID           string `json:"browserId,omitempty"`
	PublicEncryptionKey string `json:"publicEncryptionKey"`
	PublicSigningKey    string `json:"publicSigningKey"`
	WebTTYPublicKey     string `json:"webttyPublicKey,omitempty"`
	WebTTYKeyID         string `json:"webttyKeyId,omitempty"`
	WebTTYKeyAlgorithm  string `json:"webttyKeyAlgorithm,omitempty"`
	Fingerprint         string `json:"fingerprint"`
	ProofSignature      string `json:"proofSignature"`
}

type CreateWorkspaceDeviceKeyResponse struct {
	DeviceKeyID string `json:"deviceKeyId"`
	Status      string `json:"status"`
}

type WorkspaceDeviceAccessProof struct {
	DeviceFingerprint string `json:"deviceFingerprint"`
	Challenge         string `json:"challenge"`
	SignedAt          string `json:"signedAt"`
	Signature         string `json:"signature"`
}

type LookupWorkspaceDeviceKeysRequest struct {
	Proofs []WorkspaceDeviceAccessProof `json:"proofs"`
}

type WorkspaceDeviceKey struct {
	ID                  string  `json:"id"`
	Kind                string  `json:"kind"`
	Status              string  `json:"status"`
	Label               *string `json:"label,omitempty"`
	BrowserID           *string `json:"browserId,omitempty"`
	PublicEncryptionKey string  `json:"publicEncryptionKey"`
	PublicSigningKey    *string `json:"publicSigningKey,omitempty"`
	WebTTYPublicKey     *string `json:"webttyPublicKey,omitempty"`
	WebTTYKeyID         *string `json:"webttyKeyId,omitempty"`
	WebTTYKeyAlgorithm  *string `json:"webttyKeyAlgorithm,omitempty"`
	Fingerprint         string  `json:"fingerprint"`
	ApprovedAt          *string `json:"approvedAt,omitempty"`
	RevokedAt           *string `json:"revokedAt,omitempty"`
	LostAt              *string `json:"lostAt,omitempty"`
	LastUsedAt          *string `json:"lastUsedAt,omitempty"`
	CreatedAt           string  `json:"createdAt"`
}

type WorkspaceKeyEnvelopeCrypto struct {
	Suite           string         `json:"suite"`
	KeyID           string         `json:"keyId,omitempty"`
	EncapsulatedKey string         `json:"encapsulatedKey,omitempty"`
	Context         map[string]any `json:"context,omitempty"`
}

type WorkspaceKeyEnvelope struct {
	ID            string                     `json:"id"`
	KeysetID      string                     `json:"keysetId"`
	RecipientKind string                     `json:"recipientKind"`
	RecipientID   string                     `json:"recipientId"`
	Ciphertext    string                     `json:"ciphertext"`
	Crypto        WorkspaceKeyEnvelopeCrypto `json:"crypto"`
	CreatedAt     string                     `json:"createdAt"`
	RevokedAt     *string                    `json:"revokedAt,omitempty"`
}

type LookupWorkspaceDeviceKeysResponse struct {
	Devices         []WorkspaceDeviceKey   `json:"devices"`
	DeviceEnvelopes []WorkspaceKeyEnvelope `json:"deviceEnvelopes"`
}

type RevokeWorkspaceDeviceKeyRequest struct {
	Reason           string `json:"reason,omitempty"`
	MarkLost         bool   `json:"markLost,omitempty"`
	ActorDeviceKeyID string `json:"actorDeviceKeyId,omitempty"`
	Signature        string `json:"signature,omitempty"`
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

type ProjectEventsParams struct {
	Timeline     string
	Start        string
	End          string
	EventType    string
	AfterEventID string
	Page         *int
	PageSize     *int
	Order        string
}

type ProjectEvent struct {
	ID            string               `json:"id,omitempty"`
	EventID       string               `json:"eventId"`
	EventType     string               `json:"eventType"`
	EventCategory ProjectEventCategory `json:"eventCategory"`
	ProjectID     string               `json:"projectId"`
	WorkspaceID   string               `json:"workspaceId"`
	ClusterID     string               `json:"clusterId"`
	UserID        *string              `json:"userId,omitempty"`
	Payload       json.RawMessage      `json:"payload,omitempty"`
	CreatedAt     string               `json:"createdAt"`
	UpdatedAt     string               `json:"updatedAt"`
	ExpiresAt     *string              `json:"expiresAt,omitempty"`
}

type ProjectEventsResponse struct {
	Events     []ProjectEvent `json:"events"`
	Page       int            `json:"page"`
	PageSize   int            `json:"pageSize"`
	Total      int            `json:"total"`
	TotalPages int            `json:"totalPages"`
}

type ProjectEventCategory string

const (
	ProjectEventCategoryLifecycle ProjectEventCategory = "lifecycle"
	ProjectEventCategoryStreamLog ProjectEventCategory = "stream_log"
)

type ProjectWebhookDestinationType string

const (
	ProjectWebhookDestinationEndpoint          ProjectWebhookDestinationType = "webhook_endpoint"
	ProjectWebhookDestinationAmazonEventBridge ProjectWebhookDestinationType = "amazon_eventbridge"
	ProjectWebhookDestinationAzureEventGrid    ProjectWebhookDestinationType = "azure_event_grid"
)

type ProjectWebhookEndpointStatus string

const (
	ProjectWebhookEndpointEnabled  ProjectWebhookEndpointStatus = "enabled"
	ProjectWebhookEndpointDisabled ProjectWebhookEndpointStatus = "disabled"
)

type ProjectWebhookDeliveryStatus string

const (
	ProjectWebhookDeliveryPending    ProjectWebhookDeliveryStatus = "pending"
	ProjectWebhookDeliveryDelivering ProjectWebhookDeliveryStatus = "delivering"
	ProjectWebhookDeliveryRetrying   ProjectWebhookDeliveryStatus = "retrying"
	ProjectWebhookDeliverySucceeded  ProjectWebhookDeliveryStatus = "succeeded"
	ProjectWebhookDeliveryFailed     ProjectWebhookDeliveryStatus = "failed"
	ProjectWebhookDeliveryCanceled   ProjectWebhookDeliveryStatus = "canceled"
)

type ProjectWebhookDeliveryAttemptStatus string

const (
	ProjectWebhookDeliveryAttemptSucceeded    ProjectWebhookDeliveryAttemptStatus = "succeeded"
	ProjectWebhookDeliveryAttemptFailed       ProjectWebhookDeliveryAttemptStatus = "failed"
	ProjectWebhookDeliveryAttemptTimeout      ProjectWebhookDeliveryAttemptStatus = "timeout"
	ProjectWebhookDeliveryAttemptNetworkError ProjectWebhookDeliveryAttemptStatus = "network_error"
)

type ProjectWebhookEndpointConfig struct {
	URL string `json:"url"`
}

type ProjectWebhookAmazonEventBridgeConfig struct {
	EventBusArn string `json:"eventBusArn,omitempty"`
	Region      string `json:"region,omitempty"`
}

type ProjectWebhookAzureEventGridConfig struct {
	TopicEndpoint string `json:"topicEndpoint,omitempty"`
}

type ProjectWebhook struct {
	ID                      string                        `json:"id"`
	WorkspaceID             string                        `json:"workspaceId"`
	ProjectID               string                        `json:"projectId"`
	Name                    string                        `json:"name"`
	Description             *string                       `json:"description"`
	DestinationType         ProjectWebhookDestinationType `json:"destinationType"`
	Status                  ProjectWebhookEndpointStatus  `json:"status"`
	Events                  []string                      `json:"events"`
	Config                  json.RawMessage               `json:"config"`
	SigningSecret           string                        `json:"signingSecret,omitempty"`
	SecretLastRotatedAt     *string                       `json:"secretLastRotatedAt"`
	PreviousSecretExpiresAt *string                       `json:"previousSecretExpiresAt"`
	CreatedByUserID         *string                       `json:"createdByUserId"`
	CreatedAt               string                        `json:"createdAt"`
	UpdatedAt               string                        `json:"updatedAt"`
	DeletedAt               *string                       `json:"deletedAt"`
}

func (w ProjectWebhook) DecodeEndpointConfig() (ProjectWebhookEndpointConfig, error) {
	var config ProjectWebhookEndpointConfig
	if w.DestinationType != ProjectWebhookDestinationEndpoint {
		return config, fmt.Errorf("project webhook destination %q is not an HTTP endpoint", w.DestinationType)
	}
	if err := json.Unmarshal(w.Config, &config); err != nil {
		return config, err
	}
	return config, nil
}

type ProjectWebhookResponseHeaders map[string][]string

type ProjectWebhookDeliveryAttempt struct {
	ID              string                              `json:"id"`
	DeliveryID      string                              `json:"deliveryId"`
	AttemptNumber   int                                 `json:"attemptNumber"`
	Status          ProjectWebhookDeliveryAttemptStatus `json:"status"`
	HTTPStatus      *int                                `json:"httpStatus"`
	ResponseTimeMs  *int                                `json:"responseTimeMs"`
	ResponseHeaders *ProjectWebhookResponseHeaders      `json:"responseHeaders"`
	ResponseBody    *string                             `json:"responseBody"`
	ErrorCode       *string                             `json:"errorCode"`
	ErrorMessage    *string                             `json:"errorMessage"`
	StartedAt       string                              `json:"startedAt"`
	CompletedAt     *string                             `json:"completedAt"`
	CreatedAt       string                              `json:"createdAt"`
}

type ProjectWebhookDelivery struct {
	ID                 string                          `json:"id"`
	WorkspaceID        string                          `json:"workspaceId"`
	ProjectID          string                          `json:"projectId"`
	WebhookEndpointID  string                          `json:"webhookEndpointId"`
	EventID            string                          `json:"eventId"`
	EventType          string                          `json:"eventType"`
	Status             ProjectWebhookDeliveryStatus    `json:"status"`
	AttemptCount       int                             `json:"attemptCount"`
	NextAttemptAt      *string                         `json:"nextAttemptAt"`
	LastAttemptAt      *string                         `json:"lastAttemptAt"`
	SucceededAt        *string                         `json:"succeededAt"`
	FailedAt           *string                         `json:"failedAt"`
	LastHTTPStatus     *int                            `json:"lastHttpStatus"`
	LastResponseTimeMs *int                            `json:"lastResponseTimeMs"`
	LastError          *string                         `json:"lastError"`
	RequestBody        json.RawMessage                 `json:"requestBody"`
	CreatedAt          string                          `json:"createdAt"`
	UpdatedAt          string                          `json:"updatedAt"`
	Attempts           []ProjectWebhookDeliveryAttempt `json:"attempts,omitempty"`
}

type CreateProjectWebhookRequest struct {
	Name            string                        `json:"name"`
	Description     *string                       `json:"description,omitempty"`
	DestinationType ProjectWebhookDestinationType `json:"destinationType,omitempty"`
	Status          ProjectWebhookEndpointStatus  `json:"status,omitempty"`
	Events          []string                      `json:"events"`
	Config          ProjectWebhookEndpointConfig  `json:"config"`
}

type UpdateProjectWebhookRequest struct {
	Name        *string                       `json:"name,omitempty"`
	Description *string                       `json:"description,omitempty"`
	Status      ProjectWebhookEndpointStatus  `json:"status,omitempty"`
	Events      []string                      `json:"events,omitempty"`
	Config      *ProjectWebhookEndpointConfig `json:"config,omitempty"`
}

type ProjectWebhooksParams struct {
	Query           string
	Status          ProjectWebhookEndpointStatus
	DestinationType ProjectWebhookDestinationType
	Page            *int
	PageSize        *int
	Sort            string
	Order           string
}

type ProjectWebhooksResponse struct {
	Webhooks   []ProjectWebhook `json:"webhooks"`
	Page       int              `json:"page"`
	PageSize   int              `json:"pageSize"`
	Total      int              `json:"total"`
	TotalPages int              `json:"totalPages"`
}

type ProjectWebhookDeliveriesParams struct {
	Status    ProjectWebhookDeliveryStatus
	EventType string
	Start     string
	End       string
	Page      *int
	PageSize  *int
	Order     string
}

type ProjectWebhookDeliveriesResponse struct {
	Deliveries []ProjectWebhookDelivery `json:"deliveries"`
	Page       int                      `json:"page"`
	PageSize   int                      `json:"pageSize"`
	Total      int                      `json:"total"`
	TotalPages int                      `json:"totalPages"`
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

type ProjectTCPAddress struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	ClusterID string `json:"clusterId"`
	Hostname  string `json:"hostname"`
	Port      uint32 `json:"port"`
	Address   string `json:"address"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type ListProjectTCPAddressesResponse struct {
	Addresses           []ProjectTCPAddress `json:"addresses"`
	ReservationEnabled  bool                `json:"reservationEnabled"`
	PublishedTCPEnabled bool                `json:"publishedTcpEnabled"`
	Hostname            *string             `json:"hostname"`
	Limit               int                 `json:"limit"`
	QuarantineSeconds   int                 `json:"quarantineSeconds"`
}

type ReleaseProjectTCPAddressResponse struct {
	ID         string `json:"id"`
	ReusableAt string `json:"reusableAt"`
}

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
