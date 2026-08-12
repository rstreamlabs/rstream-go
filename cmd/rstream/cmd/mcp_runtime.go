// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
)

func mcpProjectSelectorProperties(extra map[string]any) map[string]any {
	props := map[string]any{"project": mcpStringSchema("Optional tunnel project name, endpoint, or ID."), "project_id": mcpStringSchema("Optional tunnel project ID."), "project_endpoint": mcpStringSchema("Optional tunnel project endpoint."), "project_name": mcpStringSchema("Optional tunnel project name.")}
	for key, value := range extra {
		props[key] = value
	}
	return props
}

func resolveMCPControlPlaneRuntime(ctx context.Context, requireToken bool) (*resolvedRuntime, error) {
	path, cfg, err := mcpLoadConfig()
	if err != nil {
		return nil, err
	}
	env := config.ReadEnv()
	envAPIURL := env.APIURL
	if envAPIURL == "" && len(cfg.Environments) == 1 {
		envAPIURL = cfg.Environments[0].APIURL
	}
	input := config.ResolveInput{Config: cfg, EnvAPIURL: envAPIURL, EnvContext: env.Context, EnvToken: env.Token, IgnoreDefaultContext: true, RequireToken: requireToken, ResolveToken: true}
	resolved, err := config.Resolve(input)
	if err == nil {
		return &resolvedRuntime{ConfigPath: path, Config: cfg, Resolved: resolved}, nil
	}
	if !requireToken || !mcpIsAuthenticationRequiredError(err) {
		return nil, err
	}
	return resolveMCPRuntime(ctx, false, true)
}

func mcpRuntimePrepare(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	payload, err := mcpPrepareRuntimeConfig(ctx, args)
	if err == nil {
		return mcpJSONResult(payload, false)
	}
	if !mcpIsAuthenticationRequiredError(err) {
		return nil, err
	}
	login, loginErr := mcpCreateAuthSession(ctx, args)
	if loginErr != nil {
		return nil, loginErr
	}
	return mcpJSONResult(map[string]any{"ready": false, "needs_login": true, "login": login, "message": "Approve the rstream login URL, then call rstream_runtime_prepare again with the same project."}, false)
}

func resolveMCPRuntimeForArgs(ctx context.Context, args map[string]json.RawMessage) (*resolvedRuntime, error) {
	if mcpRuntimeArgsSelectProject(args) {
		if _, err := mcpPrepareRuntimeConfig(ctx, args); err != nil {
			return nil, err
		}
		return resolveMCPRuntime(ctx, true, true)
	}
	runtime, err := resolveMCPRuntime(ctx, true, true)
	if err == nil {
		return runtime, nil
	}
	if !mcpRuntimeCanRepairError(err) {
		return nil, err
	}
	if _, prepareErr := mcpPrepareRuntimeConfig(ctx, args); prepareErr != nil {
		return nil, prepareErr
	}
	return resolveMCPRuntime(ctx, true, true)
}

func mcpPrepareRuntimeConfig(ctx context.Context, args map[string]json.RawMessage) (map[string]any, error) {
	controlRuntime, err := resolveMCPControlPlaneRuntime(ctx, true)
	if err != nil {
		return nil, err
	}
	client := newRuntimeControlPlaneClient(controlRuntime.Resolved)
	project, err := mcpResolveRuntimeProject(ctx, client, controlRuntime.Config, args)
	if err != nil {
		return nil, err
	}
	contextName, err := mcpRuntimeContextName(args, project)
	if err != nil {
		return nil, err
	}
	setDefault, err := mcpOptionalBoolArg(args, "set_default", true)
	if err != nil {
		return nil, err
	}
	contextValue, changed, err := mcpUpsertRuntimeContext(controlRuntime.ConfigPath, controlRuntime.Config, controlRuntime.Resolved.APIURL, project, contextName, setDefault)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ready": true, "changed": changed, "config_path": controlRuntime.ConfigPath, "context": redactContext(contextValue), "project": mcpRuntimeProjectPayload(project), "token_policy": "uses the long-lived rstream login credential stored in the CLI environment; no short-lived delegated token was minted"}, nil
}

func mcpResolveRuntimeProject(ctx context.Context, client *controlplane.Client, cfg config.Config, args map[string]json.RawMessage) (controlplane.Project, error) {
	selector, selectorKind, err := mcpRuntimeProjectSelector(args)
	if err != nil {
		return controlplane.Project{}, err
	}
	if selector != "" {
		return mcpFindRuntimeProject(ctx, client, selector, selectorKind)
	}
	if selected := mcpSelectedContextName(cfg); selected != "" {
		if contextValue, _, err := cfg.FindContextByName(selected); err != nil {
			return controlplane.Project{}, err
		} else if contextValue != nil && strings.TrimSpace(contextValue.ProjectEndpoint) != "" {
			return client.ResolveProjectByEndpoint(ctx, contextValue.ProjectEndpoint)
		}
	}
	projects, err := mcpListRuntimeProjects(ctx, client, "")
	if err != nil {
		return controlplane.Project{}, err
	}
	if len(projects) == 1 {
		return projects[0], nil
	}
	if len(projects) == 0 {
		return controlplane.Project{}, errors.New("no tunnel project is available for this rstream account")
	}
	return controlplane.Project{}, fmt.Errorf("multiple tunnel projects are available; choose one by name, endpoint, or ID: %s", strings.Join(mcpRuntimeProjectChoiceLabels(projects), ", "))
}

func mcpRuntimeProjectSelector(args map[string]json.RawMessage) (string, string, error) {
	for _, item := range []struct{ name, kind string }{{"project_id", "id"}, {"project_endpoint", "endpoint"}, {"project_name", "name"}, {"project", "any"}} {
		value, err := mcpOptionalStringArg(args, item.name, "")
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), item.kind, nil
		}
	}
	return "", "", nil
}

func mcpRuntimeArgsSelectProject(args map[string]json.RawMessage) bool {
	selector, _, err := mcpRuntimeProjectSelector(args)
	return err == nil && selector != ""
}

func mcpFindRuntimeProject(ctx context.Context, client *controlplane.Client, selector string, selectorKind string) (controlplane.Project, error) {
	if selectorKind == "endpoint" {
		return client.ResolveProjectByEndpoint(ctx, selector)
	}
	query := selector
	if selectorKind == "id" || selectorKind == "any" {
		query = ""
	}
	projects, err := mcpListRuntimeProjects(ctx, client, query)
	if err != nil {
		return controlplane.Project{}, err
	}
	for _, project := range projects {
		if mcpRuntimeProjectMatches(project, selector, selectorKind) {
			return project, nil
		}
	}
	if len(projects) == 1 && selectorKind == "any" {
		return projects[0], nil
	}
	if len(projects) == 0 {
		return controlplane.Project{}, fmt.Errorf("no tunnel project matches %q", selector)
	}
	return controlplane.Project{}, fmt.Errorf("multiple tunnel projects match %q; choose one by endpoint or ID: %s", selector, strings.Join(mcpRuntimeProjectChoiceLabels(projects), ", "))
}

func mcpListRuntimeProjects(ctx context.Context, client *controlplane.Client, query string) ([]controlplane.Project, error) {
	pageSize := 100
	response, err := client.ListProjects(ctx, controlplane.ListProjectsParams{Query: query, PageSize: &pageSize})
	if err != nil {
		return nil, mapControlPlaneError(err)
	}
	return response.Projects, nil
}

func mcpRuntimeProjectMatches(project controlplane.Project, selector string, selectorKind string) bool {
	selector = strings.ToLower(strings.TrimSpace(selector))
	return (selectorKind == "id" && strings.ToLower(project.ID) == selector) || (selectorKind == "name" && strings.ToLower(project.Name) == selector) || (selectorKind == "any" && (strings.ToLower(project.ID) == selector || strings.ToLower(project.Endpoint) == selector || strings.ToLower(project.Name) == selector))
}

func mcpRuntimeProjectChoiceLabels(projects []controlplane.Project) []string {
	labels := make([]string, 0, len(projects))
	for _, project := range projects {
		labels = append(labels, fmt.Sprintf("%s (%s)", project.Name, project.Endpoint))
	}
	return labels
}

func mcpRuntimeContextName(args map[string]json.RawMessage, project controlplane.Project) (string, error) {
	contextName, err := mcpOptionalStringArg(args, "context_name", "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(contextName) != "" {
		return strings.TrimSpace(contextName), nil
	}
	if strings.TrimSpace(project.Name) != "" {
		return strings.TrimSpace(project.Name), nil
	}
	return strings.TrimSpace(project.Endpoint), nil
}

func mcpUpsertRuntimeContext(path string, cfg config.Config, apiURL string, project controlplane.Project, contextName string, setDefault bool) (config.Context, bool, error) {
	engine := project.EngineAddress()
	if engine == "" {
		return config.Context{}, false, fmt.Errorf("tunnel project %q does not expose an engine address", project.Name)
	}
	envToken, err := resolveControlPlaneToken(cfg, apiURL)
	if err != nil {
		return config.Context{}, false, err
	}
	if envToken == "" {
		return config.Context{}, false, errors.New("authentication is required but not configured (run rstream login, set RSTREAM_AUTHENTICATION_TOKEN, or set RSTREAM_MTLS_CERT_FILE and RSTREAM_MTLS_KEY_FILE)")
	}
	contextValue, _, err := cfg.FindContextForAPIURL(contextName, apiURL)
	if err != nil {
		return config.Context{}, false, err
	}
	changed := false
	if contextValue == nil {
		cfg.Contexts = append(cfg.Contexts, config.Context{Name: contextName})
		contextValue = &cfg.Contexts[len(cfg.Contexts)-1]
		changed = true
	}
	if contextValue.APIURL != apiURL {
		contextValue.APIURL = apiURL
		changed = true
	}
	if contextValue.Engine != engine {
		contextValue.Engine = engine
		changed = true
	}
	if contextValue.ProjectEndpoint != project.Endpoint {
		contextValue.ProjectEndpoint = project.Endpoint
		changed = true
	}
	if contextValue.TURNDomain != project.Domain {
		contextValue.TURNDomain = project.Domain
		changed = true
	}
	if contextValue.TURNPort != project.TurnPort {
		contextValue.TURNPort = project.TurnPort
		changed = true
	}
	if contextValue.TURNSPort != project.TurnsPort {
		contextValue.TURNSPort = project.TurnsPort
		changed = true
	}
	if contextValue.Auth != nil {
		contextValue.Auth = nil
		changed = true
	}
	if setDefault && (cfg.Defaults.Context == nil || cfg.Defaults.Context.Name != contextName) {
		cfg.Defaults.Context = &config.DefaultContext{Name: contextName}
		changed = true
	}
	if changed {
		if err := config.WriteAtomic(path, cfg); err != nil {
			return config.Context{}, false, err
		}
	}
	return *contextValue, changed, nil
}

func mcpRuntimeProjectPayload(project controlplane.Project) map[string]any {
	return map[string]any{"id": project.ID, "workspace_id": project.WorkspaceID, "name": project.Name, "endpoint": project.Endpoint, "engine": project.EngineAddress(), "domain": project.Domain, "plan": project.Plan, "status": project.Status, "region": project.Region}
}

func mcpIsAuthenticationRequiredError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "authentication is required") || strings.Contains(message, "not authenticated") || strings.Contains(message, "token validation failed")
}

func mcpRuntimeCanRepairError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "token has expired") || strings.Contains(message, "engine is required") || strings.Contains(message, "context") || strings.Contains(message, "authentication is required")
}
