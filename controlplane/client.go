// See LICENSE file in the project root for license information.

package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrForbidden    = errors.New("not authorized")
	ErrUnauthorized = errors.New("not authenticated")
)

type apiErrorResponse struct {
	Error string `json:"error"`
}

type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type Client struct {
	apiURL     string
	token      string
	headers    map[string]string
	httpClient *http.Client
	logger     *slog.Logger
	configErr  error
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

func WithHeaders(headers map[string]string) Option {
	return func(c *Client) {
		c.headers, c.configErr = NormalizeHeaders(headers)
	}
}

func NewClient(apiURL, token string, opts ...Option) *Client {
	client := &Client{
		apiURL:     strings.TrimRight(strings.TrimSpace(apiURL), "/"),
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     slog.With("component", "control-plane.client"),
	}
	for _, opt := range opts {
		opt(client)
	}
	configuredHTTPClient := *client.httpClient
	configuredHTTPClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	client.httpClient = &configuredHTTPClient
	return client
}

func (c *Client) RequireToken() error {
	if strings.TrimSpace(c.token) == "" {
		return errors.New("not authenticated (run rstream login or set RSTREAM_AUTHENTICATION_TOKEN)")
	}
	return nil
}

func (c *Client) Whoami(ctx context.Context) (Whoami, error) {
	var out Whoami
	_, err := c.doJSON(ctx, http.MethodGet, "/api/whoami", nil, &out)
	return out, err
}

func (c *Client) ListWorkspaces(ctx context.Context, params ...ListWorkspacesParams) (ListWorkspacesResponse, error) {
	var out ListWorkspacesResponse
	query := url.Values{}
	if len(params) > 0 {
		if params[0].Type != "" {
			query.Set("type", params[0].Type)
		}
		if params[0].MembershipStatus != "" {
			query.Set("membershipStatus", params[0].MembershipStatus)
		}
	}
	_, err := c.doJSON(ctx, http.MethodGet, "/api/workspaces", query, &out)
	return out, err
}

func (c *Client) ListProjects(ctx context.Context, params ListProjectsParams) (ListProjectsResponse, error) {
	var out ListProjectsResponse
	query := url.Values{}
	if params.Query != "" {
		query.Set("q", params.Query)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Sort != "" {
		query.Set("sort", params.Sort)
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	_, err := c.doJSON(ctx, http.MethodGet, "/api/projects/tunnels", query, &out)
	return out, err
}

func (c *Client) ResolveProjectByEndpoint(ctx context.Context, endpoint string) (Project, error) {
	var out Project
	escaped := url.PathEscape(endpoint)
	_, err := c.doJSON(ctx, http.MethodGet, "/api/projects/tunnels/resolve/"+escaped, nil, &out)
	return out, err
}

func (c *Client) ResolveProjectByID(ctx context.Context, projectID string) (Project, error) {
	var out Project
	escaped := url.PathEscape(projectID)
	_, err := c.doJSON(ctx, http.MethodGet, "/api/projects/tunnels/"+escaped, nil, &out)
	return out, err
}

func (c *Client) ListWorkspaceProjects(ctx context.Context, workspaceID string, params ListProjectsParams) (ListProjectsResponse, error) {
	var out ListProjectsResponse
	query := url.Values{}
	if params.Query != "" {
		query.Set("q", params.Query)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Sort != "" {
		query.Set("sort", params.Sort)
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	path := "/api/workspaces/" + url.PathEscape(workspaceID) + "/projects/tunnels"
	_, err := c.doJSON(ctx, http.MethodGet, path, query, &out)
	return out, err
}

func (c *Client) ProjectCreationOptions(ctx context.Context, workspaceID string) (ProjectCreationOptionsResponse, error) {
	var out ProjectCreationOptionsResponse
	path := "/api/workspaces/" + url.PathEscape(workspaceID) + "/projects/tunnels/plan/config"
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetProjectPlan(ctx context.Context, projectID string) (ProjectPlan, error) {
	var out ProjectPlan
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/plan"
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) CreateProject(ctx context.Context, workspaceID string, request CreateProjectRequest) (Project, error) {
	var out Project
	path := "/api/workspaces/" + url.PathEscape(workspaceID) + "/projects/tunnels"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) CreateProjectCheckout(ctx context.Context, workspaceID string, request CreateProjectRequest) (CreateProjectCheckoutResponse, error) {
	var out CreateProjectCheckoutResponse
	path := "/api/workspaces/" + url.PathEscape(workspaceID) + "/projects/tunnels/payment-checkout"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) UpdateProject(ctx context.Context, projectID string, request UpdateProjectRequest) (Project, error) {
	var out Project
	path := "/api/projects/tunnels/" + url.PathEscape(projectID)
	_, err := c.doJSONBody(ctx, http.MethodPut, path, nil, request, &out)
	return out, err
}

func (c *Client) DeleteProject(ctx context.Context, projectID string) error {
	path := "/api/projects/tunnels/" + url.PathEscape(projectID)
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil, nil)
	return err
}

func (c *Client) CreateProjectTURNCredentials(ctx context.Context, projectID string) (TURNCredentials, error) {
	return c.CreateProjectTURNCredentialsWithOptions(ctx, projectID, CreateTURNCredentialsRequest{})
}

func (c *Client) CreateProjectTURNCredentialsWithOptions(ctx context.Context, projectID string, request CreateTURNCredentialsRequest) (TURNCredentials, error) {
	var out TURNCredentials
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/turn-server/credentials"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) CreateProjectTURNCredentialsByEndpoint(ctx context.Context, endpoint string) (TURNCredentials, error) {
	return c.CreateProjectTURNCredentialsByEndpointWithOptions(ctx, endpoint, CreateTURNCredentialsRequest{})
}

func (c *Client) CreateProjectTURNCredentialsByEndpointWithOptions(ctx context.Context, endpoint string, request CreateTURNCredentialsRequest) (TURNCredentials, error) {
	var out TURNCredentials
	path := "/api/projects/tunnels/resolve/" + url.PathEscape(endpoint) + "/turn-server/credentials"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) CreateToken(ctx context.Context, request CreateTokenRequest) (CreateTokenResponse, error) {
	var out CreateTokenResponse
	_, err := c.doJSONBody(ctx, http.MethodPost, "/api/tokens", nil, request, &out)
	return out, err
}

func (c *Client) ListWebTTYServers(ctx context.Context, projectID string, params ListWebTTYServersParams) (ListWebTTYServersResponse, error) {
	var out ListWebTTYServersResponse
	query := url.Values{}
	if params.Query != "" {
		query.Set("q", params.Query)
	}
	if params.Status != "" {
		query.Set("status", params.Status)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers"
	_, err := c.doJSON(ctx, http.MethodGet, path, query, &out)
	return out, err
}

func (c *Client) CreateWebTTYServer(ctx context.Context, projectID string, request CreateWebTTYServerRequest) (CreateWebTTYServerResponse, error) {
	var out CreateWebTTYServerResponse
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) GetWebTTYServer(ctx context.Context, projectID string, serverID string) (WebTTYServer, error) {
	var out WebTTYServer
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers/" + url.PathEscape(serverID)
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) UpdateWebTTYServer(ctx context.Context, projectID string, serverID string, request UpdateWebTTYServerRequest) (WebTTYServer, error) {
	var out WebTTYServer
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers/" + url.PathEscape(serverID)
	_, err := c.doJSONBody(ctx, http.MethodPatch, path, nil, request, &out)
	return out, err
}

func (c *Client) DeleteWebTTYServer(ctx context.Context, projectID string, serverID string) error {
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers/" + url.PathEscape(serverID)
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil, nil)
	return err
}

func (c *Client) EnrollWebTTYServer(ctx context.Context, projectID string, serverID string, request EnrollWebTTYServerRequest) (WebTTYServer, error) {
	var out WebTTYServer
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers/" + url.PathEscape(serverID) + "/enroll"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) ApproveWebTTYServerWorkspaceTrust(ctx context.Context, projectID string, serverID string, request ApproveWebTTYServerWorkspaceTrustRequest) (WebTTYServer, error) {
	var out WebTTYServer
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers/" + url.PathEscape(serverID) + "/workspace-trust"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) ResolveWebTTYServerClient(ctx context.Context, projectID string, serverID string, request ResolveWebTTYServerClientRequest) (ResolveWebTTYServerClientResponse, error) {
	var out ResolveWebTTYServerClientResponse
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webtty/servers/" + url.PathEscape(serverID) + "/client-resolution"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) CreateWorkspaceDeviceKey(ctx context.Context, workspaceID string, request CreateWorkspaceDeviceKeyRequest) (CreateWorkspaceDeviceKeyResponse, error) {
	var out CreateWorkspaceDeviceKeyResponse
	path := "/api/workspaces/" + url.PathEscape(workspaceID) + "/enterprise/devices"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) LookupWorkspaceDeviceKeys(ctx context.Context, workspaceID string, request LookupWorkspaceDeviceKeysRequest) (LookupWorkspaceDeviceKeysResponse, error) {
	var out LookupWorkspaceDeviceKeysResponse
	path := "/api/workspaces/" + url.PathEscape(workspaceID) + "/enterprise/devices/lookup"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) RevokeWorkspaceDeviceKey(ctx context.Context, workspaceID string, deviceKeyID string, request RevokeWorkspaceDeviceKeyRequest) error {
	path := "/api/workspaces/" + url.PathEscape(workspaceID) + "/enterprise/devices/" + url.PathEscape(deviceKeyID) + "/revoke"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, nil)
	return err
}

func (c *Client) GetProjectUsage(ctx context.Context, projectID string) (ProjectUsage, error) {
	var out ProjectUsage
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/usage"
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetProjectTURNUsage(ctx context.Context, projectID string) (ProjectTURNUsage, error) {
	var out ProjectTURNUsage
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/turn/usage"
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) ListProjectDomains(ctx context.Context, projectID string, params ListProjectDomainsParams) (ListProjectDomainsResponse, error) {
	var out ListProjectDomainsResponse
	query := url.Values{}
	if params.Query != "" {
		query.Set("q", params.Query)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Sort != "" {
		query.Set("sort", params.Sort)
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/domains"
	_, err := c.doJSON(ctx, http.MethodGet, path, query, &out)
	return out, err
}

func (c *Client) CreateProjectDomain(ctx context.Context, projectID string, request CreateProjectDomainRequest) (ProjectDomain, error) {
	var out ProjectDomain
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/domains"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) GetProjectDomain(ctx context.Context, projectID string, domainID string) (ProjectDomain, error) {
	var out ProjectDomain
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/domains/" + url.PathEscape(domainID)
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) DeleteProjectDomain(ctx context.Context, projectID string, domainID string) (ProjectDomain, error) {
	var out ProjectDomain
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/domains/" + url.PathEscape(domainID)
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil, &out)
	return out, err
}

func (c *Client) VerifyProjectDomain(ctx context.Context, projectID string, domainID string) (ProjectDomain, error) {
	var out ProjectDomain
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/domains/" + url.PathEscape(domainID) + "/verify"
	_, err := c.doJSON(ctx, http.MethodPost, path, nil, &out)
	return out, err
}

func (c *Client) GetProjectDomainConnect(ctx context.Context, projectID string, domainID string) (DomainConnectResponse, error) {
	var out DomainConnectResponse
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/domains/" + url.PathEscape(domainID) + "/domain-connect"
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) ListProjectTCPAddresses(ctx context.Context, projectID string) (ListProjectTCPAddressesResponse, error) {
	var out ListProjectTCPAddressesResponse
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/addresses"
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) ReserveProjectTCPAddress(ctx context.Context, projectID string) (ProjectTCPAddress, error) {
	var out ProjectTCPAddress
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/addresses"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, struct{}{}, &out)
	return out, err
}

func (c *Client) ReleaseProjectTCPAddress(ctx context.Context, projectID string, addressID string) (ReleaseProjectTCPAddressResponse, error) {
	var out ReleaseProjectTCPAddressResponse
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/addresses/" + url.PathEscape(addressID)
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil, &out)
	return out, err
}

func (c *Client) ListProjectLogs(ctx context.Context, projectID string, params ProjectLogsParams) (ProjectLogsResponse, error) {
	var out ProjectLogsResponse
	query := url.Values{}
	if params.Timeline != "" {
		query.Set("timeline", params.Timeline)
	}
	if params.Start != "" {
		query.Set("start", params.Start)
	}
	if params.End != "" {
		query.Set("end", params.End)
	}
	if params.EventType != "" {
		query.Set("eventType", params.EventType)
	}
	if params.AfterEventID != "" {
		query.Set("afterEventId", params.AfterEventID)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/logs"
	_, err := c.doJSON(ctx, http.MethodGet, path, query, &out)
	return out, err
}

func (c *Client) ListProjectEvents(ctx context.Context, projectID string, params ProjectEventsParams) (ProjectEventsResponse, error) {
	var out ProjectEventsResponse
	query := projectEventsQuery(params)
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/events"
	_, err := c.doJSON(ctx, http.MethodGet, path, query, &out)
	return out, err
}

func projectEventsQuery(params ProjectEventsParams) url.Values {
	query := url.Values{}
	if params.Timeline != "" {
		query.Set("timeline", params.Timeline)
	}
	if params.Start != "" {
		query.Set("start", params.Start)
	}
	if params.End != "" {
		query.Set("end", params.End)
	}
	if params.EventType != "" {
		query.Set("eventType", params.EventType)
	}
	if params.AfterEventID != "" {
		query.Set("afterEventId", params.AfterEventID)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	return query
}

func projectLogsQuery(params ProjectLogsParams) url.Values {
	query := url.Values{}
	if params.Timeline != "" {
		query.Set("timeline", params.Timeline)
	}
	if params.Start != "" {
		query.Set("start", params.Start)
	}
	if params.End != "" {
		query.Set("end", params.End)
	}
	if params.EventType != "" {
		query.Set("eventType", params.EventType)
	}
	if params.AfterEventID != "" {
		query.Set("afterEventId", params.AfterEventID)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	return query
}

func (c *Client) ListProjectWebhooks(ctx context.Context, projectID string, params ProjectWebhooksParams) (ProjectWebhooksResponse, error) {
	var out ProjectWebhooksResponse
	query := url.Values{}
	if params.Query != "" {
		query.Set("q", params.Query)
	}
	if params.Status != "" {
		query.Set("status", string(params.Status))
	}
	if params.DestinationType != "" {
		query.Set("destinationType", string(params.DestinationType))
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Sort != "" {
		query.Set("sort", params.Sort)
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webhooks"
	_, err := c.doJSON(ctx, http.MethodGet, path, query, &out)
	return out, err
}

func (c *Client) CreateProjectWebhook(ctx context.Context, projectID string, request CreateProjectWebhookRequest) (ProjectWebhook, error) {
	var out ProjectWebhook
	if request.DestinationType == "" {
		request.DestinationType = ProjectWebhookDestinationEndpoint
	}
	if err := validateProjectWebhookDestination(request.DestinationType); err != nil {
		return out, err
	}
	if err := validateProjectWebhookEndpointConfig(request.Config); err != nil {
		return out, err
	}
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webhooks"
	_, err := c.doJSONBody(ctx, http.MethodPost, path, nil, request, &out)
	return out, err
}

func (c *Client) GetProjectWebhook(ctx context.Context, projectID string, webhookID string) (ProjectWebhook, error) {
	var out ProjectWebhook
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webhooks/" + url.PathEscape(webhookID)
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) UpdateProjectWebhook(ctx context.Context, projectID string, webhookID string, request UpdateProjectWebhookRequest) (ProjectWebhook, error) {
	var out ProjectWebhook
	if request.Config != nil {
		if err := validateProjectWebhookEndpointConfig(*request.Config); err != nil {
			return out, err
		}
	}
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webhooks/" + url.PathEscape(webhookID)
	_, err := c.doJSONBody(ctx, http.MethodPatch, path, nil, request, &out)
	return out, err
}

func validateProjectWebhookDestination(destinationType ProjectWebhookDestinationType) error {
	if destinationType != ProjectWebhookDestinationEndpoint {
		return fmt.Errorf("project webhook destination %q is not available", destinationType)
	}
	return nil
}

func validateProjectWebhookEndpointConfig(config ProjectWebhookEndpointConfig) error {
	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return errors.New("project webhook endpoint URL is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("project webhook endpoint URL must use HTTPS and must not include credentials")
	}
	return nil
}

func (c *Client) DeleteProjectWebhook(ctx context.Context, projectID string, webhookID string) (ProjectWebhook, error) {
	var out ProjectWebhook
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webhooks/" + url.PathEscape(webhookID)
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil, &out)
	return out, err
}

func (c *Client) RotateProjectWebhookSecret(ctx context.Context, projectID string, webhookID string) (ProjectWebhook, error) {
	var out ProjectWebhook
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webhooks/" + url.PathEscape(webhookID) + "/secret/rotate"
	_, err := c.doJSON(ctx, http.MethodPost, path, nil, &out)
	return out, err
}

func (c *Client) ListProjectWebhookDeliveries(ctx context.Context, projectID string, webhookID string, params ProjectWebhookDeliveriesParams) (ProjectWebhookDeliveriesResponse, error) {
	var out ProjectWebhookDeliveriesResponse
	query := url.Values{}
	if params.Status != "" {
		query.Set("status", string(params.Status))
	}
	if params.EventType != "" {
		query.Set("eventType", params.EventType)
	}
	if params.Start != "" {
		query.Set("start", params.Start)
	}
	if params.End != "" {
		query.Set("end", params.End)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webhooks/" + url.PathEscape(webhookID) + "/deliveries"
	_, err := c.doJSON(ctx, http.MethodGet, path, query, &out)
	return out, err
}

func (c *Client) GetProjectWebhookDelivery(ctx context.Context, projectID string, webhookID string, deliveryID string) (ProjectWebhookDelivery, error) {
	var out ProjectWebhookDelivery
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/webhooks/" + url.PathEscape(webhookID) + "/deliveries/" + url.PathEscape(deliveryID)
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetProjectSettings(ctx context.Context, projectID string) (ProjectSettings, error) {
	var out ProjectSettings
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/settings"
	_, err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) PatchProjectSettings(ctx context.Context, projectID string, settings ProjectSettings) (ProjectSettings, error) {
	var out ProjectSettings
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/settings"
	_, err := c.doJSONBody(ctx, http.MethodPatch, path, nil, settings, &out)
	return out, err
}

func (c *Client) ResetProjectSettings(ctx context.Context, projectID string) (ProjectSettings, error) {
	var out ProjectSettings
	path := "/api/projects/tunnels/" + url.PathEscape(projectID) + "/settings"
	_, err := c.doJSON(ctx, http.MethodDelete, path, nil, &out)
	return out, err
}

func (c *Client) ListWorkspaceMembers(ctx context.Context, workspaceID string, params WorkspaceMembersParams) (WorkspaceMembersResponse, error) {
	var out WorkspaceMembersResponse
	query := url.Values{}
	if params.Query != "" {
		query.Set("q", params.Query)
	}
	if params.Page != nil {
		query.Set("page", strconv.Itoa(*params.Page))
	}
	if params.PageSize != nil {
		query.Set("pageSize", strconv.Itoa(*params.PageSize))
	}
	if params.Sort != "" {
		query.Set("sort", params.Sort)
	}
	if params.Order != "" {
		query.Set("order", params.Order)
	}
	path := "/api/workspaces/" + url.PathEscape(workspaceID) + "/members"
	_, err := c.doJSON(ctx, http.MethodGet, path, query, &out)
	return out, err
}

func (c *Client) requestURL(path string, query url.Values) (string, error) {
	if c.apiURL == "" {
		return "", errors.New("API URL is required")
	}
	fullURL := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		fullURL = c.apiURL + path
	}
	parsed, err := url.Parse(fullURL)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	for key, entries := range query {
		for _, entry := range entries {
			values.Add(key, entry)
		}
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, out any) (int, error) {
	return c.doJSONBody(ctx, method, path, query, nil, out)
}

func (c *Client) doJSONBody(ctx context.Context, method, path string, query url.Values, body any, out any) (int, error) {
	if c.configErr != nil {
		return 0, c.configErr
	}
	fullURL, err := c.requestURL(path, query)
	if err != nil {
		return 0, err
	}
	c.logger.Debug("control-plane request", "method", method, "url", fullURL)
	var reader *bytes.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(resp.Body)
		message := controlPlaneErrorMessage(resp.Status, responseBody)
		if resp.StatusCode == http.StatusUnauthorized {
			return resp.StatusCode, fmt.Errorf("%w: %s", ErrUnauthorized, message)
		}
		if resp.StatusCode == http.StatusForbidden {
			return resp.StatusCode, fmt.Errorf("%w: %s", ErrForbidden, message)
		}
		c.logger.Debug("control-plane response", "status", resp.StatusCode, "statusText", resp.Status)
		return resp.StatusCode, errors.New(message)
	}
	c.logger.Debug("control-plane response", "status", resp.StatusCode)
	if out == nil {
		return resp.StatusCode, nil
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func (c *Client) doForm(ctx context.Context, method, path string, form url.Values, out any) (int, error) {
	if c.configErr != nil {
		return 0, c.configErr
	}
	fullURL, err := c.requestURL(path, nil)
	if err != nil {
		return 0, err
	}
	c.logger.Debug("control-plane request", "method", method, "url", fullURL)
	req, err := http.NewRequestWithContext(ctx, method, fullURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(resp.Body)
		message := oauthControlPlaneErrorMessage(resp.Status, responseBody)
		c.logger.Debug("control-plane response", "status", resp.StatusCode, "statusText", resp.Status)
		return resp.StatusCode, message
	}
	c.logger.Debug("control-plane response", "status", resp.StatusCode)
	if out == nil {
		return resp.StatusCode, nil
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func controlPlaneErrorMessage(status string, body []byte) string {
	var payload apiErrorResponse
	if err := json.Unmarshal(body, &payload); err == nil {
		if message := strings.TrimSpace(payload.Error); message != "" {
			return message
		}
	}
	return fmt.Sprintf("control-plane request failed: %s", status)
}

func oauthControlPlaneErrorMessage(status string, body []byte) error {
	var payload oauthErrorResponse
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return &OAuthError{Code: payload.Error, Description: payload.ErrorDescription}
	}
	return fmt.Errorf("control-plane request failed: %s", status)
}
