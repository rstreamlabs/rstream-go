// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

func mcpLoadConfig() (string, config.Config, error) {
	env := config.ReadEnv()
	path := env.ConfigPath
	if path == "" {
		var err error
		path, err = config.DefaultConfigPath()
		if err != nil {
			return "", config.Config{}, err
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", config.Config{}, err
	}
	return path, cfg, nil
}

func mcpControlPlaneClient() (*controlplane.Client, *resolvedRuntime, error) {
	runtime, err := resolveMCPRuntime(false, true)
	if err != nil {
		return nil, nil, err
	}
	return controlplane.NewClient(runtime.Resolved.APIURL, runtime.Resolved.Token), runtime, nil
}

func mcpContextList(args map[string]json.RawMessage) (map[string]any, error) {
	_, cfg, err := mcpLoadConfig()
	if err != nil {
		return nil, err
	}
	contexts := append([]config.Context(nil), cfg.Contexts...)
	sort.SliceStable(contexts, func(i, j int) bool {
		if contexts[i].Name == contexts[j].Name {
			return contexts[i].APIURL < contexts[j].APIURL
		}
		return contexts[i].Name < contexts[j].Name
	})
	out := make([]config.Context, 0, len(contexts))
	for _, ctx := range contexts {
		out = append(out, redactContext(ctx))
	}
	defaultName := ""
	if cfg.Defaults.Context != nil {
		defaultName = cfg.Defaults.Context.Name
	}
	return mcpJSONResult(map[string]any{"default": defaultName, "selected": mcpSelectedContextName(cfg), "contexts": out}, false)
}

func mcpContextGet(args map[string]json.RawMessage) (map[string]any, error) {
	_, cfg, err := mcpLoadConfig()
	if err != nil {
		return nil, err
	}
	name, err := mcpOptionalStringArg(args, "name", "")
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = mcpSelectedContextName(cfg)
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("no context name provided and no default context is configured")
	}
	ctx, _, err := cfg.FindContextByName(name)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("context %q not found", name)
	}
	return mcpJSONResult(redactContext(*ctx), false)
}

func mcpRuntimeStatus() (map[string]any, error) {
	path, cfg, err := mcpLoadConfig()
	if err != nil {
		return nil, err
	}
	runtime, resolveErr := resolveMCPRuntime(false, false)
	defaultContext := ""
	if cfg.Defaults.Context != nil {
		defaultContext = cfg.Defaults.Context.Name
	}
	payload := map[string]any{"config_path": path, "default_context": defaultContext, "selected_context": mcpSelectedContextName(cfg), "contexts": len(cfg.Contexts)}
	if resolveErr != nil {
		payload["ready"] = false
		payload["error"] = resolveErr.Error()
		return mcpJSONResult(payload, false)
	}
	payload["api_url"] = runtime.Resolved.APIURL
	payload["engine"] = runtime.Resolved.Engine
	payload["has_token"] = runtime.Resolved.Token != ""
	payload["ready"] = runtime.Resolved.Token != ""
	if runtime.Resolved.Token == "" {
		payload["needs_login"] = true
	}
	if runtime.Resolved.Context != nil {
		payload["project_endpoint"] = runtime.Resolved.Context.ProjectEndpoint
	}
	return mcpJSONResult(payload, false)
}

func mcpSelectedContextName(cfg config.Config) string {
	envContext := strings.TrimSpace(config.ReadEnv().Context)
	if envContext != "" {
		return envContext
	}
	if cfg.Defaults.Context != nil {
		return cfg.Defaults.Context.Name
	}
	return ""
}

func mcpWorkspaceList(ctx context.Context) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	workspaces, err := client.ListWorkspaces(ctx)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(workspaces, false)
}

func mcpProjectList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	params, err := mcpListProjectsParams(args)
	if err != nil {
		return nil, err
	}
	workspaceID, err := mcpOptionalStringArg(args, "workspace_id", "")
	if err != nil {
		return nil, err
	}
	var projects controlplane.ListProjectsResponse
	if workspaceID == "" {
		projects, err = client.ListProjects(ctx, params)
	} else {
		projects, err = client.ListWorkspaceProjects(ctx, workspaceID, params)
	}
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(projects, false)
}

func mcpProjectCreationOptions(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	workspaceID, err := mcpRequiredStringArg(args, "workspace_id")
	if err != nil {
		return nil, err
	}
	options, err := client.ProjectCreationOptions(ctx, workspaceID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(options, false)
}

func mcpProjectCreate(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	workspaceID, request, err := mcpCreateProjectArgs(args)
	if err != nil {
		return nil, err
	}
	startCheckout, err := mcpOptionalBoolArg(args, "start_checkout", false)
	if err != nil {
		return nil, err
	}
	if startCheckout {
		checkout, err := client.CreateProjectCheckout(ctx, workspaceID, request)
		if err != nil {
			return nil, mapControlPlaneError(err)
		}
		return mcpJSONResult(map[string]any{"action": "stripe_checkout", "checkout": checkout}, false)
	}
	project, err := client.CreateProject(ctx, workspaceID, request)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(map[string]any{"action": "created", "project": project}, false)
}

func mcpProjectLogs(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	params, err := mcpProjectLogsParams(args)
	if err != nil {
		return nil, err
	}
	logs, err := client.ListProjectLogs(ctx, projectID, params)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(logs, false)
}

func mcpProjectUsage(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	usage, err := client.GetProjectUsage(ctx, projectID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(usage, false)
}

func mcpProjectPlanGet(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	plan, err := client.GetProjectPlan(ctx, projectID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(plan, false)
}

func mcpProjectTURNUsage(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	usage, err := client.GetProjectTURNUsage(ctx, projectID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(usage, false)
}

func mcpProjectTURNCredentialsCreate(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpOptionalStringArg(args, "project_id", "")
	if err != nil {
		return nil, err
	}
	projectEndpoint, err := mcpOptionalStringArg(args, "project_endpoint", "")
	if err != nil {
		return nil, err
	}
	if projectID == "" && projectEndpoint == "" {
		return nil, fmt.Errorf("missing argument %q or %q", "project_id", "project_endpoint")
	}
	ttlSeconds, err := mcpOptionalIntArg(args, "ttl_seconds")
	if err != nil {
		return nil, err
	}
	request := controlplane.CreateTURNCredentialsRequest{TTLSeconds: ttlSeconds}
	var credentials controlplane.TURNCredentials
	if projectID != "" {
		credentials, err = client.CreateProjectTURNCredentialsWithOptions(ctx, projectID, request)
	} else {
		credentials, err = client.CreateProjectTURNCredentialsByEndpointWithOptions(ctx, projectEndpoint, request)
	}
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(credentials, false)
}

func mcpProjectDomainsList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	params, err := mcpListProjectDomainsParams(args)
	if err != nil {
		return nil, err
	}
	domains, err := client.ListProjectDomains(ctx, projectID, params)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(domains, false)
}

func mcpProjectDomainCreate(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	hostname, err := mcpRequiredStringArg(args, "hostname")
	if err != nil {
		return nil, err
	}
	domain, err := client.CreateProjectDomain(ctx, projectID, controlplane.CreateProjectDomainRequest{Hostname: hostname})
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(domain, false)
}

func mcpProjectDomainGet(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, projectID, domainID, err := mcpProjectDomainClientAndIDs(args)
	if err != nil {
		return nil, err
	}
	domain, err := client.GetProjectDomain(ctx, projectID, domainID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(domain, false)
}

func mcpProjectDomainDelete(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, projectID, domainID, err := mcpProjectDomainClientAndIDs(args)
	if err != nil {
		return nil, err
	}
	domain, err := client.DeleteProjectDomain(ctx, projectID, domainID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(domain, false)
}

func mcpProjectDomainVerify(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, projectID, domainID, err := mcpProjectDomainClientAndIDs(args)
	if err != nil {
		return nil, err
	}
	domain, err := client.VerifyProjectDomain(ctx, projectID, domainID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(domain, false)
}

func mcpProjectDomainConnect(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, projectID, domainID, err := mcpProjectDomainClientAndIDs(args)
	if err != nil {
		return nil, err
	}
	response, err := client.GetProjectDomainConnect(ctx, projectID, domainID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(response, false)
}

func mcpProjectSettingsGet(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	settings, err := client.GetProjectSettings(ctx, projectID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(settings, false)
}

func mcpProjectSettingsPatch(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	settings, err := mcpProjectSettingsPatchArg(args)
	if err != nil {
		return nil, err
	}
	updated, err := client.PatchProjectSettings(ctx, projectID, settings)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(updated, false)
}

func mcpProjectSettingsReset(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, err
	}
	settings, err := client.ResetProjectSettings(ctx, projectID)
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(settings, false)
}

func mcpTokenCreate(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	permissions, err := mcpRequiredStringSliceArg(args, "permissions")
	if err != nil {
		return nil, err
	}
	request := controlplane.CreateTokenRequest{Permissions: &permissions}
	resourcesJSON, err := mcpOptionalStringArg(args, "resources_json", "")
	if err != nil {
		return nil, err
	}
	if resourcesJSON != "" {
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(resourcesJSON), &raw); err != nil {
			return mcpJSONResult(mcpTokenCreateErrorPayload("resources_json must be valid JSON."), true)
		}
		request.Resources = &raw
	}
	response, err := client.CreateToken(ctx, request)
	if err != nil {
		return mcpJSONResult(mcpTokenCreateErrorPayload(mapControlPlaneError(err).Error()), true)
	}
	return mcpJSONResult(response, false)
}

func mcpTokenCreateErrorPayload(message string) map[string]any {
	payload := map[string]any{"ok": false, "error": message}
	if !mcpTokenCreateErrorNeedsResourceHelp(message) {
		return payload
	}
	payload["hint"] = "resources_json must be the full token resources object. Match scopes.tunnels actions to the requested Engine permissions."
	payload["scope_permissions"] = map[string]string{
		"tunnels.resources.read-only":   "list",
		"tunnels.streams.create-delete": "connect",
		"tunnels.tunnels.create-delete": "create",
	}
	payload["examples"] = map[string]string{
		"connect_project_tunnels": `{"tunnels":{"projects":["PROJECT_ID"],"scopes":{"tunnels":{"connect":true}}}}`,
		"create_project_tunnels":  `{"tunnels":{"projects":["PROJECT_ID"],"scopes":{"tunnels":{"create":true}}}}`,
		"read_only_project":       `{"tunnels":{"projects":["PROJECT_ID"],"scopes":{"tunnels":{"list":true}}}}`,
	}
	return payload
}

func mcpTokenCreateErrorNeedsResourceHelp(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "invalid input") || strings.Contains(message, "resource") || strings.Contains(message, "resources_json") || strings.Contains(message, "scope")
}

func mcpWorkspaceMembersList(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, err
	}
	workspaceID, err := mcpRequiredStringArg(args, "workspace_id")
	if err != nil {
		return nil, err
	}
	params, err := mcpWorkspaceMembersParams(args)
	if err != nil {
		return nil, err
	}
	members, err := client.ListWorkspaceMembers(ctx, workspaceID, params)
	if err != nil {
		if strings.Contains(err.Error(), "Only organization workspaces have members") {
			return mcpJSONResult(mcpPersonalWorkspaceMembersPayload(workspaceID), false)
		}
		return nil, mapControlPlaneError(err)
	}
	return mcpJSONResult(members, false)
}

func mcpPersonalWorkspaceMembersPayload(workspaceID string) map[string]any {
	return map[string]any{
		"members":        []any{},
		"message":        "Personal workspaces do not have separate workspace members.",
		"ok":             true,
		"page":           1,
		"page_size":      0,
		"total":          0,
		"total_pages":    0,
		"workspace_id":   workspaceID,
		"workspace_type": "personal",
	}
}

func mcpListProjectsParams(args map[string]json.RawMessage) (controlplane.ListProjectsParams, error) {
	var params controlplane.ListProjectsParams
	var err error
	if params.Query, err = mcpOptionalStringArg(args, "q", ""); err != nil {
		return params, err
	}
	if params.Page, err = mcpOptionalIntArg(args, "page"); err != nil {
		return params, err
	}
	if params.PageSize, err = mcpOptionalIntArg(args, "page_size"); err != nil {
		return params, err
	}
	if params.Sort, err = mcpOptionalStringArg(args, "sort", ""); err != nil {
		return params, err
	}
	if params.Order, err = mcpOptionalStringArg(args, "order", ""); err != nil {
		return params, err
	}
	return params, nil
}

func mcpListProjectDomainsParams(args map[string]json.RawMessage) (controlplane.ListProjectDomainsParams, error) {
	var params controlplane.ListProjectDomainsParams
	var err error
	if params.Query, err = mcpOptionalStringArg(args, "q", ""); err != nil {
		return params, err
	}
	if params.Page, err = mcpOptionalIntArg(args, "page"); err != nil {
		return params, err
	}
	if params.PageSize, err = mcpOptionalIntArg(args, "page_size"); err != nil {
		return params, err
	}
	if params.Sort, err = mcpOptionalStringArg(args, "sort", ""); err != nil {
		return params, err
	}
	if params.Order, err = mcpOptionalStringArg(args, "order", ""); err != nil {
		return params, err
	}
	return params, nil
}

func mcpProjectLogsParams(args map[string]json.RawMessage) (controlplane.ProjectLogsParams, error) {
	var params controlplane.ProjectLogsParams
	var err error
	if params.Timeline, err = mcpOptionalStringArg(args, "timeline", ""); err != nil {
		return params, err
	}
	if params.Start, err = mcpOptionalStringArg(args, "start", ""); err != nil {
		return params, err
	}
	if params.End, err = mcpOptionalStringArg(args, "end", ""); err != nil {
		return params, err
	}
	if params.EventType, err = mcpOptionalStringArg(args, "event_type", ""); err != nil {
		return params, err
	}
	if params.AfterEventID, err = mcpOptionalStringArg(args, "after_event_id", ""); err != nil {
		return params, err
	}
	if params.Page, err = mcpOptionalIntArg(args, "page"); err != nil {
		return params, err
	}
	if params.PageSize, err = mcpOptionalIntArg(args, "page_size"); err != nil {
		return params, err
	}
	if params.Order, err = mcpOptionalStringArg(args, "order", ""); err != nil {
		return params, err
	}
	return params, nil
}

func mcpWorkspaceMembersParams(args map[string]json.RawMessage) (controlplane.WorkspaceMembersParams, error) {
	var params controlplane.WorkspaceMembersParams
	var err error
	if params.Query, err = mcpOptionalStringArg(args, "q", ""); err != nil {
		return params, err
	}
	if params.Page, err = mcpOptionalIntArg(args, "page"); err != nil {
		return params, err
	}
	if params.PageSize, err = mcpOptionalIntArg(args, "page_size"); err != nil {
		return params, err
	}
	if params.Sort, err = mcpOptionalStringArg(args, "sort", ""); err != nil {
		return params, err
	}
	if params.Order, err = mcpOptionalStringArg(args, "order", ""); err != nil {
		return params, err
	}
	return params, nil
}

func mcpProjectSettingsPatchArg(args map[string]json.RawMessage) (controlplane.ProjectSettings, error) {
	if raw, ok := args["settings"]; ok {
		var settings controlplane.ProjectSettings
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, fmt.Errorf("argument %q must be an object", "settings")
		}
		if settings == nil {
			return nil, fmt.Errorf("argument %q must be an object", "settings")
		}
		return settings, nil
	}
	settingsJSON, err := mcpRequiredStringArg(args, "settings_json")
	if err != nil {
		return nil, err
	}
	var settings controlplane.ProjectSettings
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return nil, fmt.Errorf("invalid settings_json: %w", err)
	}
	if settings == nil {
		return nil, fmt.Errorf("settings_json must be an object")
	}
	return settings, nil
}

func mcpProjectDomainClientAndIDs(args map[string]json.RawMessage) (*controlplane.Client, string, string, error) {
	client, _, err := mcpControlPlaneClient()
	if err != nil {
		return nil, "", "", err
	}
	projectID, err := mcpRequiredStringArg(args, "project_id")
	if err != nil {
		return nil, "", "", err
	}
	domainID, err := mcpRequiredStringArg(args, "domain_id")
	if err != nil {
		return nil, "", "", err
	}
	return client, projectID, domainID, nil
}

func mcpCreateProjectArgs(args map[string]json.RawMessage) (string, controlplane.CreateProjectRequest, error) {
	workspaceID, err := mcpRequiredStringArg(args, "workspace_id")
	if err != nil {
		return "", controlplane.CreateProjectRequest{}, err
	}
	request := controlplane.CreateProjectRequest{}
	if request.Name, err = mcpRequiredStringArg(args, "name"); err != nil {
		return "", request, err
	}
	if request.Provider, err = mcpRequiredStringArg(args, "provider"); err != nil {
		return "", request, err
	}
	if request.Region, err = mcpRequiredStringArg(args, "region"); err != nil {
		return "", request, err
	}
	if request.Plan, err = mcpRequiredStringArg(args, "plan"); err != nil {
		return "", request, err
	}
	if request.CreationFingerprint, err = mcpRequiredStringArg(args, "creation_fingerprint"); err != nil {
		return "", request, err
	}
	if request.IdempotencyKey, err = mcpOptionalStringArg(args, "idempotency_key", ""); err != nil {
		return "", request, err
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = mcpIdempotencyKey()
	}
	return workspaceID, request, nil
}

func mcpIdempotencyKey() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("mcp:%d", time.Now().UnixNano())
	}
	return "mcp:" + hex.EncodeToString(buf[:])
}
