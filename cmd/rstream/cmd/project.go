// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rstreamlabs/rstream-go/config"
	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	GroupID:      "management",
	Use:          "project",
	Short:        "Manage projects",
	SilenceUsage: true,
	RunE:         func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var projectListCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List projects",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		runtime, err := resolveRuntime(cmd, false, true)
		if err != nil {
			return err
		}
		client := controlplane.NewClient(runtime.Resolved.APIURL, runtime.Resolved.Token)
		if err := client.RequireToken(); err != nil {
			return err
		}
		params, sortRequested, err := listProjectsParamsFromFlags(cmd)
		if err != nil {
			return err
		}
		workspaceID, err := projectListWorkspaceFromFlags(cmd)
		if err != nil {
			return err
		}
		var resp controlplane.ListProjectsResponse
		if workspaceID == "" {
			resp, err = client.ListProjects(cmd.Context(), params)
		} else {
			resp, err = client.ListWorkspaceProjects(cmd.Context(), workspaceID, params)
		}
		if err != nil {
			return mapControlPlaneError(err)
		}
		output, _ := cmd.Flags().GetString("output")
		switch output {
		case "table":
			projects := append([]controlplane.Project(nil), resp.Projects...)
			if !sortRequested {
				sort.SliceStable(projects, func(i, j int) bool {
					if projects[i].Name == projects[j].Name {
						return projects[i].Endpoint < projects[j].Endpoint
					}
					return projects[i].Name < projects[j].Name
				})
			}
			return writeProjectsTable(cmd.OutOrStdout(), projects)
		case "json", "yaml":
			return writeStructuredOutput(output, resp)
		default:
			return validateOutputMode(output, "table", "json", "yaml")
		}
	},
}

var projectUseCmd = &cobra.Command{
	Use:          "use <project-endpoint>",
	Short:        "Set the default context from a project",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runtime, err := resolveRuntime(cmd, false, true)
		if err != nil {
			return err
		}
		client := controlplane.NewClient(runtime.Resolved.APIURL, runtime.Resolved.Token)
		if err := client.RequireToken(); err != nil {
			return err
		}
		if _, err := client.Whoami(cmd.Context()); err != nil {
			return mapControlPlaneError(err)
		}
		project, err := client.ResolveProjectByEndpoint(cmd.Context(), args[0])
		if err != nil {
			return mapControlPlaneError(err)
		}
		apiURL := runtime.Resolved.APIURL
		nameFlag, _ := cmd.Flags().GetString("name")
		ctx, err := persistProjectContext(runtime.ConfigPath, apiURL, project, nameFlag, true)
		if err != nil {
			return err
		}
		output, _ := cmd.Flags().GetString("output")
		return writeOptionalStructuredOutput(output, map[string]any{
			"project": project,
			"context": redactContext(ctx),
			"default": true,
		})
	},
}

func init() {
	projectCmd.Flags().SortFlags = false
	projectCmd.PersistentFlags().SortFlags = false
	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectUseCmd)
	projectListCmd.Flags().SortFlags = false
	projectListCmd.Flags().String("workspace", "", "workspace ID")
	projectListCmd.Flags().String("q", "", "search query")
	projectListCmd.Flags().Int("page", 0, "page number (>= 1)")
	projectListCmd.Flags().Int("page-size", 0, "page size (1-100)")
	projectListCmd.Flags().String("sort", "", "sort by (id, name, endpoint, status, plan, deployment)")
	projectListCmd.Flags().String("order", "", "sort order (asc, desc)")
	projectListCmd.Flags().StringP("output", "o", "table", "output mode (table, json, yaml)")
	projectUseCmd.Flags().SortFlags = false
	projectUseCmd.Flags().String("name", "", "context name (defaults to a derived name)")
	projectUseCmd.Flags().StringP("output", "o", "none", "output mode (none, json, yaml)")
	rootCmd.AddCommand(projectCmd)
}

func writeProjectsTable(out io.Writer, projects []controlplane.Project) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tENDPOINT\tSTATUS\tPLAN\tDEPLOYMENT\tPROVIDER\tREGION\tWORKSPACE ID\tID")
	for _, project := range projects {
		region := project.Region
		if region == "" {
			region = "-"
		}
		_, _ = fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			terminalSafeDefault(project.Name),
			terminalSafeDefault(project.Endpoint),
			terminalSafeDefault(project.Status),
			terminalSafeDefault(project.Plan),
			terminalSafeDefault(project.Deployment),
			terminalSafeDefault(project.Provider),
			terminalSafeDefault(region),
			terminalSafeDefault(project.WorkspaceID),
			terminalSafeDefault(project.ID),
		)
	}
	return w.Flush()
}

func listProjectsParamsFromFlags(cmd *cobra.Command) (controlplane.ListProjectsParams, bool, error) {
	var params controlplane.ListProjectsParams
	if q, _ := cmd.Flags().GetString("q"); q != "" {
		params.Query = q
	}
	sortRequested := cmd.Flags().Changed("sort")
	if cmd.Flags().Changed("page") {
		page, _ := cmd.Flags().GetInt("page")
		if page < 1 {
			return params, sortRequested, errors.New("--page must be >= 1")
		}
		params.Page = &page
	}
	if cmd.Flags().Changed("page-size") {
		pageSize, _ := cmd.Flags().GetInt("page-size")
		if pageSize < 1 || pageSize > 100 {
			return params, sortRequested, errors.New("--page-size must be between 1 and 100")
		}
		params.PageSize = &pageSize
	}
	if cmd.Flags().Changed("sort") {
		value, _ := cmd.Flags().GetString("sort")
		if !isAllowed(value, "id", "name", "endpoint", "status", "plan", "deployment") {
			return params, sortRequested, fmt.Errorf("invalid --sort %q", value)
		}
		params.Sort = value
	}
	if cmd.Flags().Changed("order") {
		value, _ := cmd.Flags().GetString("order")
		if !isAllowed(value, "asc", "desc") {
			return params, sortRequested, fmt.Errorf("invalid --order %q", value)
		}
		params.Order = value
	}
	return params, sortRequested, nil
}

func projectListWorkspaceFromFlags(cmd *cobra.Command) (string, error) {
	if !cmd.Flags().Changed("workspace") {
		return "", nil
	}
	workspaceID, _ := cmd.Flags().GetString("workspace")
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return "", errors.New("--workspace must not be empty")
	}
	return workspaceID, nil
}

func isAllowed(value string, allowed ...string) bool {
	for _, v := range allowed {
		if value == v {
			return true
		}
	}
	return false
}

func mapControlPlaneError(err error) error {
	if errors.Is(err, controlplane.ErrUnauthorized) {
		return errors.New("not authenticated (run rstream login or set RSTREAM_AUTHENTICATION_TOKEN)")
	}
	if errors.Is(err, controlplane.ErrForbidden) {
		return errors.New("not authorized (check token permissions and project access)")
	}
	return err
}

func findContextByProjectEndpoint(cfg *config.Config, apiURL, endpoint string) *config.Context {
	for i := range cfg.Contexts {
		ctx := &cfg.Contexts[i]
		if config.NormalizeAPIURL(ctx.APIURL) == config.NormalizeAPIURL(apiURL) && ctx.ProjectEndpoint == endpoint {
			return ctx
		}
	}
	return nil
}

func persistProjectContext(path, apiURL string, project controlplane.Project, name string, setDefault bool) (config.Context, error) {
	var persisted config.Context
	err := config.UpdateAtomic(path, func(cfg *config.Config) error {
		ctx, err := upsertProjectContext(cfg, apiURL, project, name, setDefault)
		if err != nil {
			return err
		}
		persisted = *ctx
		return nil
	})
	return persisted, err
}

func upsertProjectContext(cfg *config.Config, apiURL string, project controlplane.Project, name string, setDefault bool) (*config.Context, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	apiURL = config.NormalizeAPIURL(apiURL)
	if apiURL == "" {
		return nil, errors.New("project context requires a Control Plane API URL")
	}
	engine := project.EngineAddress()
	if engine == "" {
		return nil, fmt.Errorf("project %q does not expose an engine address", project.Name)
	}
	cfg.EnsureEnvironment(apiURL)
	ctx, err := selectProjectContextForUpsert(cfg, apiURL, project, name)
	if err != nil {
		return nil, err
	}
	ctx.APIURL = apiURL
	ctx.ProjectEndpoint = project.Endpoint
	ctx.Engine = engine
	ctx.TURNDomain = project.Domain
	ctx.TURNPort = project.TurnPort
	ctx.TURNSPort = project.TurnsPort
	if setDefault {
		cfg.Defaults.Context = &config.DefaultContext{Name: ctx.Name}
	}
	return ctx, nil
}

func selectProjectContextForUpsert(cfg *config.Config, apiURL string, project controlplane.Project, name string) (*config.Context, error) {
	name = strings.TrimSpace(name)
	if name != "" {
		existing, _, err := cfg.FindContextByName(name)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if config.NormalizeAPIURL(existing.APIURL) != apiURL {
				if strings.TrimSpace(existing.APIURL) == "" {
					return nil, fmt.Errorf("context %q already exists (unlinked)", name)
				}
				return nil, fmt.Errorf("context %q already exists for API URL %q", name, existing.APIURL)
			}
			return existing, nil
		}
		cfg.Contexts = append(cfg.Contexts, config.Context{Name: name, APIURL: apiURL})
		return &cfg.Contexts[len(cfg.Contexts)-1], nil
	}
	if existing := findContextByProjectEndpoint(cfg, apiURL, project.Endpoint); existing != nil {
		return existing, nil
	}
	baseName := slugifyName(project.Name)
	if baseName == "" {
		baseName = slugifyName(endpointPrefix(project.Endpoint))
	}
	if baseName == "" {
		baseName = "project"
	}
	uniqueName := uniqueContextName(baseName, cfg)
	cfg.Contexts = append(cfg.Contexts, config.Context{Name: uniqueName, APIURL: apiURL})
	return &cfg.Contexts[len(cfg.Contexts)-1], nil
}

func uniqueContextName(base string, cfg *config.Config) string {
	if base == "" {
		base = "context"
	}
	if !contextNameExists(cfg, base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !contextNameExists(cfg, candidate) {
			return candidate
		}
	}
}

func contextNameExists(cfg *config.Config, name string) bool {
	for i := range cfg.Contexts {
		ctx := cfg.Contexts[i]
		if ctx.Name == name {
			return true
		}
	}
	return false
}

func slugifyName(value string) string {
	lower := strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func endpointPrefix(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	idx := strings.IndexAny(endpoint, ".:")
	if idx == -1 {
		return endpoint
	}
	return endpoint[:idx]
}
