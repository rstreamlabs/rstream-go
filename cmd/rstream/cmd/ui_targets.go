// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

type uiTargetKind string

const (
	uiTargetContext uiTargetKind = "context"
	uiTargetProject uiTargetKind = "project"
)

type uiRuntimeOptions struct {
	apiURLScope     string
	contextOverride string
	region          string
	tunnelTransport string
	environment     config.EnvSettings
}

type uiRuntimeResolver struct {
	configPath string
	options    uiRuntimeOptions
}

type uiTarget struct {
	Kind          uiTargetKind
	APIURL        string
	Context       config.Context
	Project       controlplane.Project
	WorkspaceName string
	Current       bool
	Default       bool
}

type uiTargetDiscovery struct {
	Targets          []uiTarget
	ProjectError     error
	WorkspaceWarning error
}

func uiRuntimeOptionsFromCommand(cmd *cobra.Command) uiRuntimeOptions {
	env := config.ReadEnv()
	flagAPIURL, _ := cmd.Flags().GetString("api-url")
	flagContext, _ := cmd.Flags().GetString("context")
	flagRegion, _ := cmd.Flags().GetString("region")
	flagTunnelTransport, _ := cmd.Flags().GetString("tunnel-transport")
	return uiRuntimeOptions{
		apiURLScope:     config.NormalizeAPIURL(uiFirstNonEmpty(flagAPIURL, env.APIURL)),
		contextOverride: uiFirstNonEmpty(flagContext, env.Context),
		region:          uiFirstNonEmpty(flagRegion, env.Region),
		tunnelTransport: strings.TrimSpace(flagTunnelTransport),
		environment:     env,
	}
}

func newUIRuntimeResolver(path string, options uiRuntimeOptions) *uiRuntimeResolver {
	return &uiRuntimeResolver{configPath: path, options: options}
}

func (r *uiRuntimeResolver) initialTargets(cfg config.Config, runtime *resolvedRuntime) []uiTarget {
	return r.contextTargets(cfg, runtime)
}

func (r *uiRuntimeResolver) discoverTargets(ctx context.Context, runtime *resolvedRuntime) uiTargetDiscovery {
	cfg, err := config.Load(r.configPath)
	if err != nil {
		fallback := config.Config{}
		if runtime != nil {
			fallback = runtime.Config
		}
		return uiTargetDiscovery{Targets: r.contextTargets(fallback, runtime), ProjectError: fmt.Errorf("reload config: %w", err)}
	}
	discovery := uiTargetDiscovery{Targets: r.contextTargets(cfg, runtime)}
	apiURL, token, err := r.controlPlaneCredentials(cfg, runtime)
	if err != nil {
		discovery.ProjectError = err
		return discovery
	}
	environment, _ := cfg.FindEnvironment(apiURL)
	headers, err := config.ResolveControlPlaneHeaders(environment, r.options.environment.ControlPlaneHeaders)
	if err != nil {
		discovery.ProjectError = err
		return discovery
	}
	client := controlplane.NewClient(apiURL, token, controlplane.WithHeaders(headers))
	workspaces, workspaceErr := client.ListWorkspaces(ctx)
	if workspaceErr != nil {
		discovery.WorkspaceWarning = fmt.Errorf("workspace names unavailable: %w", mapControlPlaneError(workspaceErr))
	}
	projects, err := listAllUIProjects(ctx, client)
	if err != nil {
		discovery.ProjectError = err
		return discovery
	}
	workspaceNames := make(map[string]string, len(workspaces.Workspaces))
	for _, workspace := range workspaces.Workspaces {
		workspaceNames[workspace.ID] = workspace.Name
	}
	for _, project := range projects {
		discovery.Targets = append(discovery.Targets, r.projectTarget(cfg, runtime, apiURL, project, workspaceNames[project.WorkspaceID]))
	}
	sortUITargets(discovery.Targets)
	return discovery
}

func (r *uiRuntimeResolver) contextTargets(cfg config.Config, runtime *resolvedRuntime) []uiTarget {
	targets := make([]uiTarget, 0, len(cfg.Contexts))
	for _, contextValue := range cfg.Contexts {
		apiURL := config.NormalizeAPIURL(contextValue.APIURL)
		if r.options.apiURLScope != "" && apiURL != "" && apiURL != r.options.apiURLScope {
			continue
		}
		target := uiTarget{Kind: uiTargetContext, APIURL: apiURL, Context: contextValue}
		target.Current = uiContextMatchesRuntime(contextValue, runtime)
		target.Default = cfg.Defaults.Context != nil && cfg.Defaults.Context.Name == contextValue.Name
		targets = append(targets, target)
	}
	sortUITargets(targets)
	return targets
}

func (r *uiRuntimeResolver) projectTarget(cfg config.Config, runtime *resolvedRuntime, apiURL string, project controlplane.Project, workspaceName string) uiTarget {
	target := uiTarget{Kind: uiTargetProject, APIURL: apiURL, Project: project, WorkspaceName: workspaceName}
	target.Current = uiProjectMatchesRuntime(apiURL, project, runtime)
	if contextValue := findContextByProjectEndpoint(&cfg, apiURL, project.Endpoint); contextValue != nil {
		target.Context = *contextValue
		target.Default = cfg.Defaults.Context != nil && cfg.Defaults.Context.Name == contextValue.Name
	}
	return target
}

func (r *uiRuntimeResolver) prepareTarget(ctx context.Context, target uiTarget, persist bool) (*resolvedRuntime, uiConnectionInfo, string, bool, error) {
	warning := ""
	persisted := false
	if persist {
		persistedTarget, err := r.persistTarget(target)
		if err != nil {
			return nil, uiConnectionInfo{}, "", false, err
		}
		target = persistedTarget
		persisted = true
		if r.options.contextOverride != "" && r.options.contextOverride != target.Context.Name {
			warning = fmt.Sprintf("Default saved, but RSTREAM_CONTEXT or --context still selects %s in new processes", r.options.contextOverride)
		}
	}
	cfg, err := config.Load(r.configPath)
	if err != nil {
		return nil, uiConnectionInfo{}, warning, persisted, fmt.Errorf("reload config: %w", err)
	}
	contextValue, projectName, err := r.contextForTarget(cfg, target)
	if err != nil {
		return nil, uiConnectionInfo{}, warning, persisted, err
	}
	runtime, err := r.resolveContext(ctx, cfg, contextValue)
	if err != nil {
		return nil, uiConnectionInfo{}, warning, persisted, err
	}
	isDefault := cfg.Defaults.Context != nil && cfg.Defaults.Context.Name == contextValue.Name
	connection := uiConnectionInfo{ContextName: contextValue.Name, ProjectName: projectName, APIURL: runtime.Resolved.APIURL, Engine: runtime.Resolved.Engine, SessionOnly: !isDefault}
	return runtime, connection, warning, persisted, nil
}

func (r *uiRuntimeResolver) persistTarget(target uiTarget) (uiTarget, error) {
	if target.Kind == uiTargetProject {
		contextValue, err := persistProjectContext(r.configPath, target.APIURL, target.Project, "", r.options.region, true)
		if err != nil {
			return uiTarget{}, err
		}
		target.Context = contextValue
		target.Default = true
		return target, nil
	}
	var persisted config.Context
	err := config.UpdateAtomic(r.configPath, func(cfg *config.Config) error {
		contextValue, err := findUITargetContext(cfg, target.Context)
		if err != nil {
			return err
		}
		cfg.Defaults.Context = &config.DefaultContext{Name: contextValue.Name}
		persisted = *contextValue
		return nil
	})
	if err != nil {
		return uiTarget{}, err
	}
	target.Context = persisted
	target.Default = true
	return target, nil
}

func (r *uiRuntimeResolver) contextForTarget(cfg config.Config, target uiTarget) (config.Context, string, error) {
	if target.Kind == uiTargetContext {
		contextValue, err := findUITargetContext(&cfg, target.Context)
		if err != nil {
			return config.Context{}, "", err
		}
		return *contextValue, "", nil
	}
	contextValue, err := upsertProjectContext(&cfg, target.APIURL, target.Project, target.Context.Name, r.options.region, false)
	if err != nil {
		return config.Context{}, "", err
	}
	return *contextValue, target.Project.Name, nil
}

func (r *uiRuntimeResolver) resolveContext(ctx context.Context, cfg config.Config, contextValue config.Context) (*resolvedRuntime, error) {
	if r.options.apiURLScope != "" && contextValue.APIURL != "" && config.NormalizeAPIURL(contextValue.APIURL) != r.options.apiURLScope {
		return nil, fmt.Errorf("context %q belongs to API URL %q, outside the selected API URL %q", contextValue.Name, contextValue.APIURL, r.options.apiURLScope)
	}
	if engineOverride := strings.TrimSpace(r.options.environment.Engine); engineOverride != "" && strings.TrimSpace(contextValue.Engine) != engineOverride {
		return nil, fmt.Errorf("cannot switch to context %q while RSTREAM_ENGINE selects %q", contextValue.Name, engineOverride)
	}
	isolated := cfg
	isolated.Contexts = []config.Context{contextValue}
	isolated.Defaults.Context = nil
	env := r.options.environment
	selectedAPIURL := config.NormalizeAPIURL(contextValue.APIURL)
	if selectedAPIURL == "" {
		selectedAPIURL = r.options.apiURLScope
	}
	input := config.ResolveInput{
		Config:                 isolated,
		FlagAPIURL:             selectedAPIURL,
		FlagContext:            contextValue.Name,
		EnvEngine:              env.Engine,
		EnvToken:               env.Token,
		EnvMTLSCert:            env.MTLSCert,
		EnvMTLSKey:             env.MTLSKey,
		FlagRegion:             r.options.region,
		EnvControlPlaneHeaders: env.ControlPlaneHeaders,
		FlagTunnelTransport:    r.options.tunnelTransport,
		EnvTunnelTransport:     env.TunnelTransport,
		EnvUseQUIC:             env.UseQUIC,
		RequireEngine:          true,
		RequireToken:           true,
		ResolveToken:           true,
	}
	resolved, err := config.Resolve(input)
	if err != nil {
		return nil, fmt.Errorf("resolve context %q: %w", contextValue.Name, err)
	}
	if resolved.Region != "" {
		if err := resolveRuntimeRegionContext(ctx, cfg, &resolved); err != nil {
			return nil, fmt.Errorf("resolve context %q region: %w", contextValue.Name, err)
		}
	}
	return &resolvedRuntime{ConfigPath: r.configPath, Config: cfg, Resolved: resolved}, nil
}

func (r *uiRuntimeResolver) controlPlaneCredentials(cfg config.Config, runtime *resolvedRuntime) (string, string, error) {
	apiURL := r.options.apiURLScope
	if apiURL == "" && runtime != nil && runtime.Resolved.Context != nil {
		apiURL = config.NormalizeAPIURL(runtime.Resolved.Context.APIURL)
	}
	if apiURL == "" {
		return "", "", errors.New("control plane is not configured for the current context; local contexts remain available")
	}
	env := r.options.environment
	resolved, err := config.Resolve(config.ResolveInput{Config: cfg, FlagAPIURL: apiURL, EnvToken: env.Token, IgnoreDefaultContext: true, RequireToken: true, ResolveToken: true})
	if err == nil && strings.TrimSpace(resolved.Token) != "" {
		return apiURL, resolved.Token, nil
	}
	if runtime != nil && config.NormalizeAPIURL(runtime.Resolved.APIURL) == apiURL && strings.TrimSpace(runtime.Resolved.Token) != "" {
		return apiURL, runtime.Resolved.Token, nil
	}
	if err != nil {
		return "", "", fmt.Errorf("control plane unavailable for %s: %w; local contexts remain available", apiURL, err)
	}
	return "", "", fmt.Errorf("control plane unavailable for %s: authentication is not configured; local contexts remain available", apiURL)
}

func listAllUIProjects(ctx context.Context, client *controlplane.Client) ([]controlplane.Project, error) {
	const pageSize = 100
	projects := make([]controlplane.Project, 0)
	for page := 1; ; page++ {
		response, err := client.ListProjects(ctx, controlplane.ListProjectsParams{Page: &page, PageSize: intPtr(pageSize), Sort: "name", Order: "asc"})
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", mapControlPlaneError(err))
		}
		projects = append(projects, response.Projects...)
		if response.TotalPages <= page || len(response.Projects) == 0 {
			return projects, nil
		}
	}
}

func findUITargetContext(cfg *config.Config, selected config.Context) (*config.Context, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	matches := make([]int, 0, 1)
	selectedAPIURL := config.NormalizeAPIURL(selected.APIURL)
	for index := range cfg.Contexts {
		candidate := cfg.Contexts[index]
		if candidate.Name == selected.Name && config.NormalizeAPIURL(candidate.APIURL) == selectedAPIURL {
			matches = append(matches, index)
		}
	}
	if len(matches) == 1 {
		return &cfg.Contexts[matches[0]], nil
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("context %q is no longer configured", selected.Name)
	}
	exact := make([]int, 0, 1)
	for _, index := range matches {
		candidate := cfg.Contexts[index]
		if candidate.Engine == selected.Engine && candidate.ProjectEndpoint == selected.ProjectEndpoint {
			exact = append(exact, index)
		}
	}
	if len(exact) == 1 {
		return &cfg.Contexts[exact[0]], nil
	}
	return nil, fmt.Errorf("context %q is ambiguous for API URL %q", selected.Name, selectedAPIURL)
}

func uiContextMatchesRuntime(contextValue config.Context, runtime *resolvedRuntime) bool {
	if runtime == nil || runtime.Resolved.Context == nil {
		return false
	}
	current := runtime.Resolved.Context
	return current.Name == contextValue.Name && config.NormalizeAPIURL(current.APIURL) == config.NormalizeAPIURL(contextValue.APIURL) && current.Engine == contextValue.Engine && current.ProjectEndpoint == contextValue.ProjectEndpoint
}

func uiProjectMatchesRuntime(apiURL string, project controlplane.Project, runtime *resolvedRuntime) bool {
	if runtime == nil || runtime.Resolved.Context == nil {
		return false
	}
	current := runtime.Resolved.Context
	return config.NormalizeAPIURL(current.APIURL) == config.NormalizeAPIURL(apiURL) && current.ProjectEndpoint == project.Endpoint
}

func sortUITargets(targets []uiTarget) {
	sort.SliceStable(targets, func(left, right int) bool {
		if targets[left].Kind != targets[right].Kind {
			return targets[left].Kind == uiTargetContext
		}
		leftName := strings.ToLower(targets[left].displayName())
		rightName := strings.ToLower(targets[right].displayName())
		if leftName != rightName {
			return leftName < rightName
		}
		return targets[left].stableID() < targets[right].stableID()
	})
}

func (t uiTarget) stableID() string {
	if t.Kind == uiTargetProject {
		return strings.Join([]string{string(t.Kind), config.NormalizeAPIURL(t.APIURL), t.Project.ID, t.Project.Endpoint}, "|")
	}
	return strings.Join([]string{string(t.Kind), config.NormalizeAPIURL(t.Context.APIURL), t.Context.Name, t.Context.Engine, t.Context.ProjectEndpoint}, "|")
}

func (t uiTarget) displayName() string {
	if t.Kind == uiTargetProject {
		return uiFirstNonEmpty(strings.TrimSpace(t.Project.Name), strings.TrimSpace(t.Project.Endpoint), strings.TrimSpace(t.Project.ID))
	}
	return t.Context.Name
}

func (t uiTarget) searchText() string {
	return strings.ToLower(strings.Join([]string{string(t.Kind), t.APIURL, t.Context.Name, t.Context.Engine, t.Context.ProjectEndpoint, t.Project.ID, t.Project.Name, t.Project.Endpoint, t.Project.WorkspaceID, t.WorkspaceName, t.Project.Status}, " "))
}

func intPtr(value int) *int {
	return &value
}

func uiFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
