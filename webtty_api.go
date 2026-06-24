// See LICENSE file in the project root for license information.

package rstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type WebTTYTransport string

const (
	WebTTYTransportPlain        WebTTYTransport = "plain"
	WebTTYTransportWebSocket    WebTTYTransport = "websocket"
	WebTTYTransportWebTransport WebTTYTransport = "webtransport"
)

type WebTTYRecordingMode string

const (
	WebTTYRecordingModeRecorded WebTTYRecordingMode = "recorded"
	WebTTYRecordingModePrivate  WebTTYRecordingMode = "private"
)

type WebTTYEncryptionMode string

const (
	WebTTYEncryptionModeManaged WebTTYEncryptionMode = "managed"
	WebTTYEncryptionModeE2E     WebTTYEncryptionMode = "e2e"
)

type WebTTYSessionMode string

const (
	WebTTYSessionModeInteractive    WebTTYSessionMode = "interactive"
	WebTTYSessionModeNonInteractive WebTTYSessionMode = "non-interactive"
)

type WebTTYSessionStatus string

const (
	WebTTYSessionStatusOpening WebTTYSessionStatus = "opening"
	WebTTYSessionStatusActive  WebTTYSessionStatus = "active"
	WebTTYSessionStatusClosing WebTTYSessionStatus = "closing"
	WebTTYSessionStatusClosed  WebTTYSessionStatus = "closed"
	WebTTYSessionStatusErrored WebTTYSessionStatus = "errored"
)

type WebTTYParticipantRole string

const (
	WebTTYParticipantRoleController WebTTYParticipantRole = "controller"
	WebTTYParticipantRoleSpectator  WebTTYParticipantRole = "spectator"
)

type WebTTYControlRequestStatus string

const (
	WebTTYControlRequestPending WebTTYControlRequestStatus = "pending"
	WebTTYControlRequestGranted WebTTYControlRequestStatus = "granted"
	WebTTYControlRequestRefused WebTTYControlRequestStatus = "refused"
	WebTTYControlRequestRevoked WebTTYControlRequestStatus = "revoked"
	WebTTYControlRequestExpired WebTTYControlRequestStatus = "expired"
)

type WebTTYOrigin string

const (
	WebTTYOriginHuman        WebTTYOrigin = "human"
	WebTTYOriginCodex        WebTTYOrigin = "codex"
	WebTTYOriginAPI          WebTTYOrigin = "api"
	WebTTYOriginAutomation   WebTTYOrigin = "automation"
	WebTTYOriginCI           WebTTYOrigin = "ci"
	WebTTYOriginScheduledJob WebTTYOrigin = "scheduled-job"
)

type WebTTYInitiatorKind string

const (
	WebTTYInitiatorUser           WebTTYInitiatorKind = "user"
	WebTTYInitiatorApp            WebTTYInitiatorKind = "app"
	WebTTYInitiatorAgent          WebTTYInitiatorKind = "agent"
	WebTTYInitiatorServiceAccount WebTTYInitiatorKind = "service-account"
)

type WebTTYEventDirection string

const (
	WebTTYDirectionClientToServer WebTTYEventDirection = "client_to_server"
	WebTTYDirectionServerToClient WebTTYEventDirection = "server_to_client"
	WebTTYDirectionEngineInternal WebTTYEventDirection = "engine_internal"
)

type WebTTYStreamType string

const (
	WebTTYStreamTypeStdin  WebTTYStreamType = "stdin"
	WebTTYStreamTypeStdout WebTTYStreamType = "stdout"
	WebTTYStreamTypeStderr WebTTYStreamType = "stderr"
)

type WebTTYEventType string

const (
	WebTTYSessionEventOpen           WebTTYEventType = "open"
	WebTTYSessionEventAck            WebTTYEventType = "ack"
	WebTTYSessionEventData           WebTTYEventType = "data"
	WebTTYSessionEventResize         WebTTYEventType = "resize"
	WebTTYSessionEventClose          WebTTYEventType = "close"
	WebTTYSessionEventError          WebTTYEventType = "error"
	WebTTYSessionEventParticipant    WebTTYEventType = "participant"
	WebTTYSessionEventControl        WebTTYEventType = "control"
	WebTTYSessionEventRecordingState WebTTYEventType = "recording_state"
)

type WebTTYCapabilities struct {
	ManagedProtocol       bool                   `json:"managed_protocol"`
	StoreConfigured       bool                   `json:"store_configured"`
	SessionListing        bool                   `json:"session_listing"`
	Recording             bool                   `json:"recording"`
	Replay                bool                   `json:"replay"`
	LiveAttach            bool                   `json:"live_attach"`
	ControlTransfer       bool                   `json:"control_transfer"`
	E2E                   bool                   `json:"e2e"`
	ImplementedTransports []WebTTYTransport      `json:"implemented_transports"`
	RecordingModes        []WebTTYRecordingMode  `json:"recording_modes"`
	EncryptionModes       []WebTTYEncryptionMode `json:"encryption_modes"`
	RequiredPermissions   map[string][]string    `json:"required_permissions"`
}

type WebTTYCryptoMetadata struct {
	PayloadSuite     string          `json:"payload_suite,omitempty"`
	PayloadKeyID     string          `json:"payload_key_id,omitempty"`
	Nonce            string          `json:"nonce,omitempty"`
	KeyEnvelopeSuite string          `json:"key_envelope_suite,omitempty"`
	KeyEnvelopes     json.RawMessage `json:"key_envelopes,omitempty"`
	KeyContext       json.RawMessage `json:"key_context,omitempty"`
	KeyContextRaw    string          `json:"key_context_raw,omitempty"`
}

type WebTTYContextMetadata struct {
	Origin        WebTTYOrigin        `json:"origin,omitempty"`
	OriginID      string              `json:"origin_id,omitempty"`
	Purpose       string              `json:"purpose,omitempty"`
	InitiatorKind WebTTYInitiatorKind `json:"initiator_kind,omitempty"`
	AgentName     string              `json:"agent_name,omitempty"`
	AgentVersion  string              `json:"agent_version,omitempty"`
	RequestID     string              `json:"request_id,omitempty"`
	Labels        map[string]string   `json:"labels,omitempty"`
}

type WebTTYSessionLive struct {
	Available               bool   `json:"available"`
	Attachable              bool   `json:"attachable"`
	ParticipantCount        int    `json:"participant_count"`
	ControllerParticipantID string `json:"controller_participant_id,omitempty"`
	HasUpstream             bool   `json:"has_upstream"`
}

type WebTTYSession struct {
	ID              string                `json:"id"`
	WorkspaceID     string                `json:"workspace_id,omitempty"`
	ProjectID       string                `json:"project_id,omitempty"`
	ClusterID       string                `json:"cluster_id,omitempty"`
	ServerID        string                `json:"server_id,omitempty"`
	TunnelID        string                `json:"tunnel_id"`
	ClientID        string                `json:"client_id,omitempty"`
	InitiatorUserID string                `json:"initiator_user_id,omitempty"`
	GroupID         string                `json:"group_id,omitempty"`
	Status          WebTTYSessionStatus   `json:"status"`
	SessionMode     WebTTYSessionMode     `json:"session_mode"`
	RecordingMode   WebTTYRecordingMode   `json:"recording_mode"`
	EncryptionMode  WebTTYEncryptionMode  `json:"encryption_mode"`
	DownTransport   WebTTYTransport       `json:"downstream_transport,omitempty"`
	UpTransport     WebTTYTransport       `json:"upstream_transport,omitempty"`
	CommandMeta     json.RawMessage       `json:"command_meta,omitempty"`
	Context         WebTTYContextMetadata `json:"context,omitempty"`
	StartedAt       time.Time             `json:"started_at"`
	EndedAt         *time.Time            `json:"ended_at,omitempty"`
	ExitCode        *int32                `json:"exit_code,omitempty"`
	ErrorCode       string                `json:"error_code,omitempty"`
	ErrorMessage    string                `json:"error_message,omitempty"`
	Live            WebTTYSessionLive     `json:"live"`
}

type WebTTYSessionGroup struct {
	ID              string                `json:"id"`
	WorkspaceID     string                `json:"workspace_id,omitempty"`
	ProjectID       string                `json:"project_id,omitempty"`
	InitiatorUserID string                `json:"initiator_user_id,omitempty"`
	Context         WebTTYContextMetadata `json:"context,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
	ClosedAt        *time.Time            `json:"closed_at,omitempty"`
}

type WebTTYParticipant struct {
	ID                   string                `json:"id"`
	SessionID            string                `json:"session_id"`
	UserID               string                `json:"user_id,omitempty"`
	DeviceID             string                `json:"device_id,omitempty"`
	BrowserID            string                `json:"browser_id,omitempty"`
	Role                 WebTTYParticipantRole `json:"role"`
	AttachedAt           time.Time             `json:"attached_at"`
	DetachedAt           *time.Time            `json:"detached_at,omitempty"`
	Controller           bool                  `json:"controller"`
	GrantState           string                `json:"grant_state,omitempty"`
	AttachGrant          []byte                `json:"attach_grant,omitempty"`
	AttachGrantExpiresAt *time.Time            `json:"attach_grant_expires_at,omitempty"`
	Live                 WebTTYParticipantLive `json:"live"`
}

type WebTTYParticipantLive struct {
	Connected  bool `json:"connected"`
	Controller bool `json:"controller"`
}

type WebTTYSessionEvent struct {
	ID                string                `json:"id"`
	SessionID         string                `json:"session_id"`
	Seq               string                `json:"seq"`
	CreatedAt         time.Time             `json:"created_at"`
	Type              WebTTYEventType       `json:"type"`
	Direction         WebTTYEventDirection  `json:"direction,omitempty"`
	StreamType        WebTTYStreamType      `json:"stream_type,omitempty"`
	ParticipantID     string                `json:"participant_id,omitempty"`
	PayloadLength     int                   `json:"payload_length,omitempty"`
	PayloadCiphertext []byte                `json:"payload_ciphertext,omitempty"`
	PayloadPlaintext  []byte                `json:"payload_plaintext,omitempty"`
	Crypto            *WebTTYCryptoMetadata `json:"crypto,omitempty"`
	PrevHash          []byte                `json:"prev_hash,omitempty"`
	Hash              []byte                `json:"hash,omitempty"`
	Metadata          json.RawMessage       `json:"metadata,omitempty"`
}

func (e *WebTTYSessionEvent) UnmarshalJSON(data []byte) error {
	type alias WebTTYSessionEvent
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	if out.Crypto != nil && len(bytes.TrimSpace(out.Crypto.KeyEnvelopes)) > 0 {
		return fmt.Errorf("WebTTY session event crypto key_envelopes are available only from the decrypt-material endpoint")
	}
	*e = WebTTYSessionEvent(out)
	return nil
}

type WebTTYKeyGrant struct {
	ID            string               `json:"id"`
	SessionID     string               `json:"session_id"`
	RecipientID   string               `json:"recipient_id"`
	RecipientKind string               `json:"recipient_kind"`
	GrantedBy     string               `json:"granted_by,omitempty"`
	Crypto        WebTTYCryptoMetadata `json:"crypto"`
	CreatedAt     time.Time            `json:"created_at"`
	RevokedAt     *time.Time           `json:"revoked_at,omitempty"`
}

func (g *WebTTYKeyGrant) UnmarshalJSON(data []byte) error {
	type alias WebTTYKeyGrant
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	var material struct {
		WrappedKey json.RawMessage `json:"wrapped_key"`
	}
	if err := json.Unmarshal(data, &material); err != nil {
		return err
	}
	if len(bytes.TrimSpace(material.WrappedKey)) > 0 {
		return fmt.Errorf("WebTTY key grant wrapped_key is available only from the decrypt-material endpoint")
	}
	if len(bytes.TrimSpace(out.Crypto.KeyEnvelopes)) > 0 {
		return fmt.Errorf("WebTTY key grant crypto key_envelopes are available only from the decrypt-material endpoint")
	}
	*g = WebTTYKeyGrant(out)
	return nil
}

type WebTTYKeyGrantDecryptMaterial struct {
	ID            string               `json:"id"`
	SessionID     string               `json:"session_id"`
	RecipientID   string               `json:"recipient_id"`
	RecipientKind string               `json:"recipient_kind"`
	GrantedBy     string               `json:"granted_by,omitempty"`
	WrappedKey    []byte               `json:"wrapped_key,omitempty"`
	Crypto        WebTTYCryptoMetadata `json:"crypto"`
	CreatedAt     time.Time            `json:"created_at"`
	RevokedAt     *time.Time           `json:"revoked_at,omitempty"`
}

type WebTTYControlRequest struct {
	ID                     string                     `json:"id"`
	SessionID              string                     `json:"session_id"`
	RequesterParticipantID string                     `json:"requester_participant_id"`
	RequesterUserID        string                     `json:"requester_user_id,omitempty"`
	ApproverParticipantID  string                     `json:"approver_participant_id,omitempty"`
	ApproverUserID         string                     `json:"approver_user_id,omitempty"`
	Status                 WebTTYControlRequestStatus `json:"status"`
	Reason                 string                     `json:"reason,omitempty"`
	Metadata               json.RawMessage            `json:"metadata,omitempty"`
	CreatedAt              time.Time                  `json:"created_at"`
	UpdatedAt              time.Time                  `json:"updated_at"`
	ResolvedAt             *time.Time                 `json:"resolved_at,omitempty"`
	ExpiresAt              *time.Time                 `json:"expires_at,omitempty"`
}

type ListWebTTYSessionsResponse = []WebTTYSession
type ListWebTTYSessionGroupsResponse = []WebTTYSessionGroup
type ListWebTTYParticipantsResponse = []WebTTYParticipant
type ListWebTTYSessionEventsResponse = []WebTTYSessionEvent
type ListWebTTYKeyGrantsResponse = []WebTTYKeyGrant
type ListWebTTYKeyGrantDecryptMaterialResponse = []WebTTYKeyGrantDecryptMaterial
type ListWebTTYControlRequestsResponse = []WebTTYControlRequest

type ListWebTTYSessionsFilters struct {
	ServerID    *string    `json:"server_id,omitempty"`
	TunnelID    *string    `json:"tunnel_id,omitempty"`
	UserID      *string    `json:"user_id,omitempty"`
	GroupID     *string    `json:"group_id,omitempty"`
	Origin      *string    `json:"origin,omitempty"`
	Status      *string    `json:"status,omitempty"`
	StartedFrom *time.Time `json:"started_from,omitempty"`
	StartedTo   *time.Time `json:"started_to,omitempty"`
}

type ListWebTTYSessionsParams struct {
	Limit   *int                       `json:"limit,omitempty"`
	Filters *ListWebTTYSessionsFilters `json:"filters,omitempty"`
}

type ListWebTTYSessionGroupsFilters struct {
	InitiatorUserID *string    `json:"initiator_user_id,omitempty"`
	Origin          *string    `json:"origin,omitempty"`
	CreatedFrom     *time.Time `json:"created_from,omitempty"`
	CreatedTo       *time.Time `json:"created_to,omitempty"`
}

type ListWebTTYSessionGroupsParams struct {
	Limit   *int                            `json:"limit,omitempty"`
	Filters *ListWebTTYSessionGroupsFilters `json:"filters,omitempty"`
}

type ListWebTTYSessionEventsParams struct {
	FromSeq *string `json:"from_seq,omitempty"`
	Limit   *int    `json:"limit,omitempty"`
}

type ListWebTTYKeyGrantDecryptMaterialParams struct {
	RecipientID   *string `json:"recipient_id,omitempty"`
	RecipientKind *string `json:"recipient_kind,omitempty"`
}

type ListWebTTYControlRequestsFilters struct {
	Status          *string `json:"status,omitempty"`
	RequesterUserID *string `json:"requester_user_id,omitempty"`
}

type ListWebTTYControlRequestsParams struct {
	Limit   *int                              `json:"limit,omitempty"`
	Filters *ListWebTTYControlRequestsFilters `json:"filters,omitempty"`
}

type AttachWebTTYParticipantRequest struct {
	Role       string          `json:"role,omitempty"`
	DeviceID   string          `json:"device_id,omitempty"`
	BrowserID  string          `json:"browser_id,omitempty"`
	Transport  WebTTYTransport `json:"transport,omitempty"`
	GrantState string          `json:"grant_state,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type DetachWebTTYParticipantRequest struct {
	Reason   string          `json:"reason,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

type CreateWebTTYControlRequest struct {
	ParticipantID string          `json:"participant_id"`
	Reason        string          `json:"reason,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	ExpiresAt     *time.Time      `json:"expires_at,omitempty"`
}

type ResolveWebTTYControlRequest struct {
	Action                string `json:"action"`
	ApproverParticipantID string `json:"approver_participant_id,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

func (c *Client) GetWebTTYCapabilities(ctx context.Context) (*WebTTYCapabilities, error) {
	var out WebTTYCapabilities
	if err := c.webTTYAPIGet(ctx, "/webtty/capabilities", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListWebTTYSessions(ctx context.Context, params *ListWebTTYSessionsParams) (*ListWebTTYSessionsResponse, error) {
	q := url.Values{}
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	var out ListWebTTYSessionsResponse
	if err := c.webTTYAPIGet(ctx, "/webtty/sessions", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetWebTTYSession(ctx context.Context, id string) (*WebTTYSession, error) {
	if err := requireWebTTYID("session ID", id); err != nil {
		return nil, err
	}
	var out WebTTYSession
	if err := c.webTTYAPIGet(ctx, "/webtty/sessions/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListWebTTYSessionGroups(ctx context.Context, params *ListWebTTYSessionGroupsParams) (*ListWebTTYSessionGroupsResponse, error) {
	q := url.Values{}
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	var out ListWebTTYSessionGroupsResponse
	if err := c.webTTYAPIGet(ctx, "/webtty/groups", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetWebTTYSessionGroup(ctx context.Context, id string) (*WebTTYSessionGroup, error) {
	if err := requireWebTTYID("session group ID", id); err != nil {
		return nil, err
	}
	var out WebTTYSessionGroup
	if err := c.webTTYAPIGet(ctx, "/webtty/groups/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListWebTTYSessionEvents(ctx context.Context, sessionID string, params *ListWebTTYSessionEventsParams) (*ListWebTTYSessionEventsResponse, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	q := url.Values{}
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	var out ListWebTTYSessionEventsResponse
	if err := c.webTTYAPIGet(ctx, "/webtty/sessions/"+url.PathEscape(sessionID)+"/events", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListWebTTYParticipants(ctx context.Context, sessionID string) (*ListWebTTYParticipantsResponse, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	var out ListWebTTYParticipantsResponse
	if err := c.webTTYAPIGet(ctx, "/webtty/sessions/"+url.PathEscape(sessionID)+"/participants", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) AttachWebTTYParticipant(ctx context.Context, sessionID string, req AttachWebTTYParticipantRequest) (*WebTTYParticipant, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	var out WebTTYParticipant
	if err := c.webTTYAPIPost(ctx, "/webtty/sessions/"+url.PathEscape(sessionID)+"/participants", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DetachWebTTYParticipant(ctx context.Context, sessionID string, participantID string, req DetachWebTTYParticipantRequest) (*WebTTYParticipant, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	if err := requireWebTTYID("participant ID", participantID); err != nil {
		return nil, err
	}
	var out WebTTYParticipant
	path := "/webtty/sessions/" + url.PathEscape(sessionID) + "/participants/" + url.PathEscape(participantID)
	if err := c.webTTYAPIPost(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListWebTTYKeyGrants(ctx context.Context, sessionID string) (*ListWebTTYKeyGrantsResponse, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	var out ListWebTTYKeyGrantsResponse
	if err := c.webTTYAPIGet(ctx, "/webtty/sessions/"+url.PathEscape(sessionID)+"/key-grants", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListWebTTYKeyGrantDecryptMaterial(ctx context.Context, sessionID string, params *ListWebTTYKeyGrantDecryptMaterialParams) (*ListWebTTYKeyGrantDecryptMaterialResponse, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	q := url.Values{}
	if params != nil {
		if params.RecipientID != nil {
			q.Set("recipient_id", *params.RecipientID)
		}
		if params.RecipientKind != nil {
			q.Set("recipient_kind", *params.RecipientKind)
		}
	}
	var out ListWebTTYKeyGrantDecryptMaterialResponse
	path := "/webtty/sessions/" + url.PathEscape(sessionID) + "/key-grants/decrypt-material"
	if err := c.webTTYAPIGet(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListWebTTYControlRequests(ctx context.Context, sessionID string, params *ListWebTTYControlRequestsParams) (*ListWebTTYControlRequestsResponse, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	q := url.Values{}
	if err := setQueryJSON(q, "params", params); err != nil {
		return nil, err
	}
	var out ListWebTTYControlRequestsResponse
	path := "/webtty/sessions/" + url.PathEscape(sessionID) + "/control-requests"
	if err := c.webTTYAPIGet(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateWebTTYControlRequest(ctx context.Context, sessionID string, req CreateWebTTYControlRequest) (*WebTTYControlRequest, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	var out WebTTYControlRequest
	path := "/webtty/sessions/" + url.PathEscape(sessionID) + "/control-requests"
	if err := c.webTTYAPIPost(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ResolveWebTTYControlRequest(ctx context.Context, sessionID string, requestID string, req ResolveWebTTYControlRequest) (*WebTTYControlRequest, error) {
	if err := requireWebTTYID("session ID", sessionID); err != nil {
		return nil, err
	}
	if err := requireWebTTYID("control request ID", requestID); err != nil {
		return nil, err
	}
	var out WebTTYControlRequest
	path := "/webtty/sessions/" + url.PathEscape(sessionID) + "/control-requests/" + url.PathEscape(requestID)
	if err := c.webTTYAPIPost(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) webTTYAPIGet(ctx context.Context, path string, query url.Values, out any) error {
	b, _, err := c.apiDo(ctx, http.MethodGet, path, query, nil, nil, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *Client) webTTYAPIPost(ctx context.Context, path string, req any, out any) error {
	body, err := webTTYAPIJSONBody(req)
	if err != nil {
		return err
	}
	b, _, err := c.apiDo(ctx, http.MethodPost, path, nil, body, nil, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func webTTYAPIJSONBody(value any) (io.Reader, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	return bytes.NewReader(raw), nil
}

func requireWebTTYID(name string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}
