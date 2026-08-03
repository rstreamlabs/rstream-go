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

const (
	webTTYServerRecordingPolicyRecorded    = "recorded"
	webTTYServerRecordingPolicyPrivate     = "private"
	webTTYServerAccessPolicyProjectMembers = "project_members"
	webTTYServerAccessPolicyRestricted     = "restricted"
	webTTYServerStatusPendingEnrollment    = "pending_enrollment"
	webTTYServerStatusActive               = "active"
	webTTYServerStatusSuspended            = "suspended"
	webTTYServerStatusDeleted              = "deleted"
	webTTYServerAdminOutputText            = "text"
	webTTYServerAdminOutputTable           = "table"
	webTTYServerAdminOutputJSON            = "json"
	webTTYServerAdminOutputYAML            = "yaml"
)

type webTTYRegisteredServerProject struct {
	ID              string `json:"id"`
	Source          string `json:"source"`
	ProjectEndpoint string `json:"project_endpoint,omitempty"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
}

var webttyServerCreateCmd = &cobra.Command{
	Use:          "create <name>",
	Short:        "Create a registered WebTTY server",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYServerCreate(cmd, args[0])
	},
}

var webttyServerRegisteredListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List registered WebTTY servers",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYServerRegisteredList(cmd)
	},
}

var webttyServerShowCmd = &cobra.Command{
	Use:          "show <server-id>",
	Short:        "Show a registered WebTTY server",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYServerShow(cmd, args[0])
	},
}

var webttyServerDeleteCmd = &cobra.Command{
	Use:          "delete <server-id>",
	Short:        "Delete a registered WebTTY server",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYServerDelete(cmd, args[0])
	},
}

var webttyServerTrustCmd = &cobra.Command{
	Use:          "trust <server-id>",
	Short:        "Pin workspace trust for a registered WebTTY server",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWebTTYServerTrust(cmd, args[0])
	},
}

func init() {
	for _, cmd := range []*cobra.Command{
		webttyServerCreateCmd,
		webttyServerRegisteredListCmd,
		webttyServerShowCmd,
		webttyServerDeleteCmd,
	} {
		cmd.Flags().SortFlags = false
		cmd.PersistentFlags().SortFlags = false
		addWebTTYRegisteredServerProjectFlag(cmd)
	}
	webttyServerCreateCmd.Flags().String("description", "", "server description")
	webttyServerCreateCmd.Flags().String("recording-policy", webTTYServerRecordingPolicyRecorded, "recording policy (recorded, private)")
	webttyServerCreateCmd.Flags().String("encryption-policy", webTTYServerEncryptionPolicyDisabled, "encryption policy (disabled, explicit_key, workspace_managed)")
	webttyServerCreateCmd.Flags().String("access-policy", webTTYServerAccessPolicyProjectMembers, "access policy (project_members, restricted)")
	webttyServerCreateCmd.Flags().StringArray("label", nil, "server label (key=value, may be specified multiple times)")
	webttyServerCreateCmd.Flags().Bool("enroll", false, "enroll this server locally after creating it")
	webttyServerCreateCmd.Flags().String("identity-file", "", "local WebTTY server identity file used with --enroll")
	webttyServerCreateCmd.Flags().String("server-enrollment", "", "local registered WebTTY server enrollment file used with --enroll")
	webttyServerCreateCmd.Flags().StringP("output", "o", webTTYServerAdminOutputText, "output mode (text, json, yaml)")
	webttyServerRegisteredListCmd.Flags().String("q", "", "search query")
	webttyServerRegisteredListCmd.Flags().String("status", "", "server status (pending_enrollment, active, suspended, deleted)")
	webttyServerRegisteredListCmd.Flags().Int("page", 0, "page number (>= 1)")
	webttyServerRegisteredListCmd.Flags().Int("page-size", 0, "page size (1-100)")
	webttyServerRegisteredListCmd.Flags().StringP("output", "o", webTTYServerAdminOutputTable, "output mode (table, json, yaml)")
	webttyServerShowCmd.Flags().StringP("output", "o", webTTYServerAdminOutputTable, "output mode (table, json, yaml)")
	webttyServerDeleteCmd.Flags().Bool("yes", false, "confirm deletion")
	webttyServerDeleteCmd.Flags().StringP("output", "o", webTTYServerAdminOutputText, "output mode (text, json, yaml)")
	webttyServerTrustCmd.Flags().SortFlags = false
	webttyServerTrustCmd.PersistentFlags().SortFlags = false
	addWebTTYRegisteredServerProjectFlag(webttyServerTrustCmd)
	webttyServerTrustCmd.Flags().String("server-enrollment", "", "local registered WebTTY server enrollment file")
	webttyServerTrustCmd.Flags().StringP("output", "o", webTTYServerAdminOutputText, "output mode (text, json, yaml)")
	webttyServerCmd.AddCommand(
		webttyServerCreateCmd,
		webttyServerRegisteredListCmd,
		webttyServerShowCmd,
		webttyServerDeleteCmd,
		webttyServerTrustCmd,
	)
}

func addWebTTYRegisteredServerProjectFlag(cmd *cobra.Command) {
	cmd.Flags().String("project-id", "", "tunnels project ID (defaults to the active project)")
}

func runWebTTYServerCreate(cmd *cobra.Command, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("server name is required")
	}
	runtime, client, project, err := webTTYRegisteredServerControlPlane(cmd)
	if err != nil {
		return err
	}
	params, err := createWebTTYServerRequestFromFlags(cmd, name)
	if err != nil {
		return err
	}
	enrollLocal, _ := cmd.Flags().GetBool("enroll")
	var workspaceTrust *workspaceServerTrustMaterial
	if enrollLocal && params.EncryptionPolicy == webTTYServerEncryptionPolicyWorkspaceManaged {
		workspaceID, err := webTTYRegisteredServerProjectWorkspaceID(cmd, client, project)
		if err != nil {
			return err
		}
		material, err := workspaceTrustedDeviceForServerTrust(cmd.Context(), client, workspaceID)
		if err != nil {
			return fmt.Errorf("workspace-managed WebTTY servers require this machine to be a trusted workspace device before creation; %s: %w", workspaceDeviceEnrollmentHint(workspaceID), err)
		}
		workspaceTrust = &material
	}
	existing, found, err := existingRegisteredWebTTYServerByName(cmd, client, project.ID, name)
	if err != nil {
		return err
	}
	reusedExisting := false
	response := controlplane.CreateWebTTYServerResponse{}
	if found {
		if err := validateExistingWebTTYServerForCreate(existing, params, enrollLocal); err != nil {
			return err
		}
		reusedExisting = true
		response.Server = existing
	} else {
		response, err = client.CreateWebTTYServer(cmd.Context(), project.ID, params)
		if err != nil {
			return mapWebTTYServerCreateError(err, project.ID, name)
		}
	}
	var localEnrollment *webTTYServerEnrollmentFile
	var localEnrollmentPath string
	if enrollLocal {
		identityPath, _ := cmd.Flags().GetString("identity-file")
		enrollmentPath, err := webTTYServerEnrollmentPathFromFlags(cmd)
		if err != nil {
			return err
		}
		enrollment, path, err := enrollWebTTYServer(cmd.Context(), runtime, client, webTTYServerEnrollOptions{
			ProjectID:      project.ID,
			ServerID:       response.Server.ID,
			IdentityPath:   identityPath,
			EnrollmentPath: enrollmentPath,
			WorkspaceTrust: workspaceTrust,
		})
		if err != nil {
			return mapWebTTYServerWriteError(err)
		}
		localEnrollment = &enrollment
		localEnrollmentPath = path
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.ToLower(strings.TrimSpace(output))
	switch output {
	case webTTYServerAdminOutputText:
		if localEnrollment != nil {
			printWebTTYServerCreateEnrolledText(cmd.OutOrStdout(), project, response, localEnrollmentPath, localEnrollment, reusedExisting)
		} else {
			printWebTTYServerCreateText(cmd.OutOrStdout(), runtime, project, response)
		}
		return nil
	case webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML:
		result := webTTYServerEnrollmentCommandOutput(runtime, project, response)
		if localEnrollment != nil {
			result["local_enrollment"] = map[string]any{
				"server_enrollment":  localEnrollmentPath,
				"identity_file":      localEnrollment.IdentityFile,
				"server_fingerprint": localEnrollment.ServerFingerprint,
				"workspace_trust":    webTTYServerEnrollmentWorkspaceTrustStatus(localEnrollment),
			}
			result["reused_existing"] = reusedExisting
		}
		return writeStructuredOutput(output, result)
	default:
		return validateOutputMode(output, webTTYServerAdminOutputText, webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML)
	}
}

func runWebTTYServerRegisteredList(cmd *cobra.Command) error {
	_, client, project, err := webTTYRegisteredServerControlPlane(cmd)
	if err != nil {
		return err
	}
	params, err := listWebTTYServersParamsFromFlags(cmd)
	if err != nil {
		return err
	}
	response, err := client.ListWebTTYServers(cmd.Context(), project.ID, params)
	if err != nil {
		return mapWebTTYServerReadError(err)
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.ToLower(strings.TrimSpace(output))
	switch output {
	case webTTYServerAdminOutputTable:
		servers := append([]controlplane.WebTTYServer(nil), response.Servers...)
		sortRegisteredWebTTYServers(servers)
		return printRegisteredWebTTYServersTable(cmd.OutOrStdout(), servers)
	case webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML:
		return writeStructuredOutput(output, response)
	default:
		return validateOutputMode(output, webTTYServerAdminOutputTable, webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML)
	}
}

func runWebTTYServerShow(cmd *cobra.Command, serverID string) error {
	serverID = strings.TrimSpace(serverID)
	if err := validateWebTTYServerID(serverID); err != nil {
		return err
	}
	_, client, project, err := webTTYRegisteredServerControlPlane(cmd)
	if err != nil {
		return err
	}
	server, err := client.GetWebTTYServer(cmd.Context(), project.ID, serverID)
	if err != nil {
		return mapWebTTYServerReadError(err)
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.ToLower(strings.TrimSpace(output))
	switch output {
	case webTTYServerAdminOutputTable:
		return printRegisteredWebTTYServerDetails(cmd.OutOrStdout(), server)
	case webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML:
		return writeStructuredOutput(output, server)
	default:
		return validateOutputMode(output, webTTYServerAdminOutputTable, webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML)
	}
}

func runWebTTYServerDelete(cmd *cobra.Command, serverID string) error {
	serverID = strings.TrimSpace(serverID)
	if err := validateWebTTYServerID(serverID); err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		return fmt.Errorf("refusing to delete registered WebTTY server %q without --yes", serverID)
	}
	_, client, project, err := webTTYRegisteredServerControlPlane(cmd)
	if err != nil {
		return err
	}
	if err := client.DeleteWebTTYServer(cmd.Context(), project.ID, serverID); err != nil {
		return mapWebTTYServerWriteError(err)
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.ToLower(strings.TrimSpace(output))
	switch output {
	case webTTYServerAdminOutputText:
		fmt.Fprintf(cmd.OutOrStdout(), "Registered WebTTY server deleted: %s\n", serverID)
		return nil
	case webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML:
		return writeStructuredOutput(output, map[string]any{
			"deleted":    true,
			"server_id":  serverID,
			"project_id": project.ID,
		})
	default:
		return validateOutputMode(output, webTTYServerAdminOutputText, webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML)
	}
}

func runWebTTYServerTrust(cmd *cobra.Command, serverID string) error {
	serverID = strings.TrimSpace(serverID)
	if err := validateWebTTYServerID(serverID); err != nil {
		return err
	}
	enrollmentPath, err := webTTYServerEnrollmentPathFromFlags(cmd)
	if err != nil {
		return err
	}
	if enrollmentPath == "" {
		enrollmentPath, err = defaultWebTTYServerEnrollmentPath(serverID)
		if err != nil {
			return err
		}
	}
	enrollment, err := loadWebTTYServerEnrollmentFile(enrollmentPath)
	if err != nil {
		return err
	}
	if enrollment.ServerID != serverID {
		return fmt.Errorf("WebTTY server enrollment %s belongs to server %q", enrollmentPath, enrollment.ServerID)
	}
	if enrollment.EncryptionPolicy != webTTYServerEncryptionPolicyWorkspaceManaged {
		return fmt.Errorf("workspace trust pins require a workspace-managed WebTTY server enrollment")
	}
	projectID, _ := cmd.Flags().GetString("project-id")
	projectID = strings.TrimSpace(projectID)
	if projectID != "" && projectID != enrollment.ProjectID {
		return fmt.Errorf("WebTTY server enrollment %s belongs to project %s, but --project-id targets %s", enrollmentPath, enrollment.ProjectID, projectID)
	}
	var runtime *resolvedRuntime
	if projectID != "" {
		runtime, err = resolveControlPlane(cmd, true)
	} else {
		runtime, err = resolveRuntime(cmd, false, true)
	}
	if err != nil {
		return err
	}
	client := newRuntimeControlPlaneClient(runtime.Resolved)
	if err := maybeApproveWorkspaceManagedWebTTYServerTrust(cmd.Context(), runtime, client, enrollmentPath, enrollment, nil); err != nil {
		return mapWebTTYServerWriteError(err)
	}
	output, _ := cmd.Flags().GetString("output")
	output = strings.ToLower(strings.TrimSpace(output))
	result := map[string]any{
		"server_id":                   enrollment.ServerID,
		"workspace_id":                enrollment.WorkspaceID,
		"project_id":                  enrollment.ProjectID,
		"server_enrollment":           enrollmentPath,
		"workspace_trust_keyset_id":   enrollment.WorkspaceTrustKeysetID,
		"workspace_trust_fingerprint": enrollment.WorkspaceTrustKeysetFingerprint,
		"workspace_trust_signing_key": enrollment.WorkspaceTrustPublicSigningKey,
	}
	switch output {
	case webTTYServerAdminOutputText:
		fmt.Fprintf(cmd.OutOrStdout(), "Workspace trust pinned for WebTTY server: %s\n", enrollment.ServerID)
		fmt.Fprintf(cmd.OutOrStdout(), "Enrollment: %s\n", enrollmentPath)
		return nil
	case webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML:
		return writeStructuredOutput(output, result)
	default:
		return validateOutputMode(output, webTTYServerAdminOutputText, webTTYServerAdminOutputJSON, webTTYServerAdminOutputYAML)
	}
}

func webTTYRegisteredServerControlPlane(cmd *cobra.Command) (*resolvedRuntime, *controlplane.Client, webTTYRegisteredServerProject, error) {
	projectID, _ := cmd.Flags().GetString("project-id")
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		runtime, err := resolveControlPlane(cmd, true)
		if err != nil {
			return nil, nil, webTTYRegisteredServerProject{}, err
		}
		client := newRuntimeControlPlaneClient(runtime.Resolved)
		return runtime, client, webTTYRegisteredServerProject{ID: projectID, Source: "flag"}, nil
	}
	runtime, err := resolveRuntime(cmd, false, true)
	if err != nil {
		return nil, nil, webTTYRegisteredServerProject{}, err
	}
	client := newRuntimeControlPlaneClient(runtime.Resolved)
	project, err := webTTYRegisteredServerProjectFromFlags(cmd, runtime, client)
	if err != nil {
		return nil, nil, webTTYRegisteredServerProject{}, err
	}
	return runtime, client, project, nil
}

func webTTYRegisteredServerProjectFromFlags(cmd *cobra.Command, runtime *resolvedRuntime, client *controlplane.Client) (webTTYRegisteredServerProject, error) {
	projectID, _ := cmd.Flags().GetString("project-id")
	projectID = strings.TrimSpace(projectID)
	if projectID != "" {
		return webTTYRegisteredServerProject{ID: projectID, Source: "flag"}, nil
	}
	if runtime == nil || runtime.Resolved.Context == nil || strings.TrimSpace(runtime.Resolved.Context.ProjectEndpoint) == "" {
		return webTTYRegisteredServerProject{}, webTTYRegisteredServerProjectRequiredError()
	}
	projectEndpoint := strings.TrimSpace(runtime.Resolved.Context.ProjectEndpoint)
	project, err := client.ResolveProjectByEndpoint(cmd.Context(), projectEndpoint)
	if err != nil {
		return webTTYRegisteredServerProject{}, mapControlPlaneError(err)
	}
	projectID = strings.TrimSpace(project.ID)
	if projectID == "" {
		return webTTYRegisteredServerProject{}, fmt.Errorf("active project %s has no project ID; run rstream project list and pass --project-id", projectEndpoint)
	}
	return webTTYRegisteredServerProject{ID: projectID, Source: "active_project", ProjectEndpoint: projectEndpoint, WorkspaceID: project.WorkspaceID}, nil
}

func webTTYRegisteredServerProjectRequiredError() error {
	return fmt.Errorf("--project-id is required when no active project context is configured; run rstream project list to choose a project, or run rstream project use <project-endpoint> and retry")
}

func mapWebTTYServerReadError(err error) error {
	return mapWebTTYServerPermissionError(err, "network.webtty-servers.read-only")
}

func mapWebTTYServerWriteError(err error) error {
	return mapWebTTYServerPermissionError(err, "network.webtty-servers.read-write")
}

func mapWebTTYServerCreateError(err error, projectID string, name string) error {
	if strings.Contains(err.Error(), "WebTTY server with this name already exists") ||
		strings.Contains(err.Error(), "registered WebTTY server already exists") {
		return fmt.Errorf(
			"registered WebTTY server %q already exists in this project; run rstream webtty server list --project-id %s --q %s, then delete the existing server with rstream webtty server delete <server-id> --project-id %s --yes, or choose another name",
			name,
			shellQuote(projectID),
			shellQuote(name),
			shellQuote(projectID),
		)
	}
	return mapWebTTYServerWriteError(err)
}

func mapWebTTYServerPermissionError(err error, permission string) error {
	if errors.Is(err, controlplane.ErrForbidden) {
		return fmt.Errorf(
			"not authorized to manage registered WebTTY servers (missing %s; run rstream login to refresh CLI permissions and check project access)",
			permission,
		)
	}
	return mapControlPlaneError(err)
}

func createWebTTYServerRequestFromFlags(cmd *cobra.Command, name string) (controlplane.CreateWebTTYServerRequest, error) {
	recordingPolicy, _ := cmd.Flags().GetString("recording-policy")
	recordingPolicy = strings.ToLower(strings.TrimSpace(recordingPolicy))
	if !isAllowed(recordingPolicy, webTTYServerRecordingPolicyRecorded, webTTYServerRecordingPolicyPrivate) {
		return controlplane.CreateWebTTYServerRequest{}, fmt.Errorf("invalid --recording-policy %q", recordingPolicy)
	}
	encryptionPolicy, _ := cmd.Flags().GetString("encryption-policy")
	encryptionPolicy = strings.ToLower(strings.TrimSpace(encryptionPolicy))
	if !isAllowed(encryptionPolicy, webTTYServerEncryptionPolicyDisabled, webTTYServerEncryptionPolicyExplicitKey, webTTYServerEncryptionPolicyWorkspaceManaged) {
		return controlplane.CreateWebTTYServerRequest{}, fmt.Errorf("invalid --encryption-policy %q", encryptionPolicy)
	}
	accessPolicy, _ := cmd.Flags().GetString("access-policy")
	accessPolicy = strings.ToLower(strings.TrimSpace(accessPolicy))
	if !isAllowed(accessPolicy, webTTYServerAccessPolicyProjectMembers, webTTYServerAccessPolicyRestricted) {
		return controlplane.CreateWebTTYServerRequest{}, fmt.Errorf("invalid --access-policy %q", accessPolicy)
	}
	descriptionValue, _ := cmd.Flags().GetString("description")
	descriptionValue = strings.TrimSpace(descriptionValue)
	var description *string
	if descriptionValue != "" {
		description = &descriptionValue
	}
	labels, err := webTTYRegisteredServerLabelsFromFlags(cmd)
	if err != nil {
		return controlplane.CreateWebTTYServerRequest{}, err
	}
	return controlplane.CreateWebTTYServerRequest{
		Name:             name,
		Description:      description,
		RecordingPolicy:  recordingPolicy,
		EncryptionPolicy: encryptionPolicy,
		AccessPolicy:     accessPolicy,
		Labels:           labels,
	}, nil
}

func webTTYRegisteredServerProjectWorkspaceID(cmd *cobra.Command, client *controlplane.Client, project webTTYRegisteredServerProject) (string, error) {
	if strings.TrimSpace(project.WorkspaceID) != "" {
		return strings.TrimSpace(project.WorkspaceID), nil
	}
	if strings.TrimSpace(project.ProjectEndpoint) != "" {
		resolved, err := client.ResolveProjectByEndpoint(cmd.Context(), project.ProjectEndpoint)
		if err != nil {
			return "", mapControlPlaneError(err)
		}
		if strings.TrimSpace(resolved.WorkspaceID) != "" {
			return strings.TrimSpace(resolved.WorkspaceID), nil
		}
	}
	pageSize := 100
	projects, err := client.ListProjects(cmd.Context(), controlplane.ListProjectsParams{PageSize: &pageSize})
	if err != nil {
		return "", mapControlPlaneError(err)
	}
	for _, candidate := range projects.Projects {
		if candidate.ID == project.ID && strings.TrimSpace(candidate.WorkspaceID) != "" {
			return strings.TrimSpace(candidate.WorkspaceID), nil
		}
	}
	return "", fmt.Errorf("project %s workspace ID could not be resolved; run rstream project use <project-endpoint> or pass a project from rstream project list", project.ID)
}

func existingRegisteredWebTTYServerByName(cmd *cobra.Command, client *controlplane.Client, projectID string, name string) (controlplane.WebTTYServer, bool, error) {
	pageSize := 100
	response, err := client.ListWebTTYServers(cmd.Context(), projectID, controlplane.ListWebTTYServersParams{
		Query:    name,
		PageSize: &pageSize,
	})
	if err != nil {
		return controlplane.WebTTYServer{}, false, mapWebTTYServerReadError(err)
	}
	for _, server := range response.Servers {
		if server.Name == name {
			return server, true, nil
		}
	}
	return controlplane.WebTTYServer{}, false, nil
}

func validateExistingWebTTYServerForCreate(server controlplane.WebTTYServer, params controlplane.CreateWebTTYServerRequest, enrollLocal bool) error {
	if !enrollLocal {
		return fmt.Errorf("registered WebTTY server %q already exists in this project; run rstream webtty server show %s, delete it with rstream webtty server delete %s --yes, or choose another name", server.Name, server.ID, server.ID)
	}
	if server.Status != webTTYServerStatusPendingEnrollment {
		return fmt.Errorf("registered WebTTY server %q already exists with status %s; run rstream webtty server show %s, delete it, or choose another name", server.Name, server.Status, server.ID)
	}
	if server.RecordingPolicy != params.RecordingPolicy ||
		server.EncryptionPolicy != params.EncryptionPolicy ||
		server.AccessPolicy != params.AccessPolicy ||
		!stringMapEqual(server.Labels, params.Labels) {
		return fmt.Errorf("registered WebTTY server %q already exists with different settings; run rstream webtty server delete %s --yes or choose another name", server.Name, server.ID)
	}
	return nil
}

func stringMapEqual(left map[string]string, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func listWebTTYServersParamsFromFlags(cmd *cobra.Command) (controlplane.ListWebTTYServersParams, error) {
	var params controlplane.ListWebTTYServersParams
	if value, _ := cmd.Flags().GetString("q"); strings.TrimSpace(value) != "" {
		params.Query = strings.TrimSpace(value)
	}
	if value, _ := cmd.Flags().GetString("status"); strings.TrimSpace(value) != "" {
		value = strings.ToLower(strings.TrimSpace(value))
		if !isAllowed(value, webTTYServerStatusPendingEnrollment, webTTYServerStatusActive, webTTYServerStatusSuspended, webTTYServerStatusDeleted) {
			return params, fmt.Errorf("invalid --status %q", value)
		}
		params.Status = value
	}
	if cmd.Flags().Changed("page") {
		page, _ := cmd.Flags().GetInt("page")
		if page < 1 {
			return params, fmt.Errorf("--page must be >= 1")
		}
		params.Page = &page
	}
	if cmd.Flags().Changed("page-size") {
		pageSize, _ := cmd.Flags().GetInt("page-size")
		if pageSize < 1 || pageSize > 100 {
			return params, fmt.Errorf("--page-size must be between 1 and 100")
		}
		params.PageSize = &pageSize
	}
	return params, nil
}

func webTTYRegisteredServerLabelsFromFlags(cmd *cobra.Command) (map[string]string, error) {
	values, _ := cmd.Flags().GetStringArray("label")
	if len(values) == 0 {
		return nil, nil
	}
	labels := map[string]string{}
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --label %q: expected key=value", value)
		}
		key := strings.TrimSpace(parts[0])
		labelValue := strings.TrimSpace(parts[1])
		if key == "" || labelValue == "" {
			return nil, fmt.Errorf("invalid --label %q: key and value are required", value)
		}
		labels[key] = labelValue
	}
	return labels, nil
}

func sortRegisteredWebTTYServers(servers []controlplane.WebTTYServer) {
	sort.SliceStable(servers, func(i, j int) bool {
		left := servers[i]
		right := servers[j]
		if left.Name == right.Name {
			return left.ID < right.ID
		}
		return left.Name < right.Name
	})
}

func printRegisteredWebTTYServersTable(out io.Writer, servers []controlplane.WebTTYServer) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSTATUS\tRECORDING\tENCRYPTION\tACCESS\tSERVER ID")
	for _, server := range servers {
		_, _ = fmt.Fprintf(
			w,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			terminalSafeDefault(server.Name),
			terminalSafeDefault(server.Status),
			terminalSafeDefault(server.RecordingPolicy),
			terminalSafeDefault(server.EncryptionPolicy),
			terminalSafeDefault(server.AccessPolicy),
			terminalSafeDefault(server.ID),
		)
	}
	return w.Flush()
}

func printRegisteredWebTTYServerDetails(out io.Writer, server controlplane.WebTTYServer) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Name\t%s\n", terminalSafeDefault(server.Name))
	_, _ = fmt.Fprintf(w, "Server ID\t%s\n", terminalSafeDefault(server.ID))
	_, _ = fmt.Fprintf(w, "Project ID\t%s\n", terminalSafeDefault(server.ProjectID))
	_, _ = fmt.Fprintf(w, "Workspace ID\t%s\n", terminalSafeDefault(server.WorkspaceID))
	_, _ = fmt.Fprintf(w, "Status\t%s\n", terminalSafeDefault(server.Status))
	_, _ = fmt.Fprintf(w, "Recording\t%s\n", terminalSafeDefault(server.RecordingPolicy))
	_, _ = fmt.Fprintf(w, "Encryption\t%s\n", terminalSafeDefault(server.EncryptionPolicy))
	_, _ = fmt.Fprintf(w, "Access\t%s\n", terminalSafeDefault(server.AccessPolicy))
	if server.ServerFingerprint != nil && strings.TrimSpace(*server.ServerFingerprint) != "" {
		_, _ = fmt.Fprintf(w, "Fingerprint\t%s\n", terminalSafeDefault(*server.ServerFingerprint))
	}
	if server.EnrolledAt != nil && strings.TrimSpace(*server.EnrolledAt) != "" {
		_, _ = fmt.Fprintf(w, "Enrolled At\t%s\n", terminalSafeDefault(*server.EnrolledAt))
	}
	return w.Flush()
}

func printWebTTYServerCreateText(out io.Writer, runtime *resolvedRuntime, project webTTYRegisteredServerProject, response controlplane.CreateWebTTYServerResponse) {
	fmt.Fprintln(out, "Registered WebTTY server created")
	fmt.Fprintf(out, "Project: %s\n", webTTYRegisteredServerProjectDescription(project))
	fmt.Fprintf(out, "Server ID: %s\n", response.Server.ID)
	fmt.Fprintf(out, "Status: %s\n", response.Server.Status)
	fmt.Fprintf(out, "Enroll command: %s\n", webTTYServerEnrollCommand(runtime, project.ID, response.Server.ID))
	fmt.Fprintf(out, "Run command: rstream webtty server -v --server-id %s --login-user <local-username>\n", response.Server.ID)
}

func printWebTTYServerCreateEnrolledText(out io.Writer, project webTTYRegisteredServerProject, response controlplane.CreateWebTTYServerResponse, enrollmentPath string, enrollment *webTTYServerEnrollmentFile, reusedExisting bool) {
	if reusedExisting {
		fmt.Fprintln(out, "Registered WebTTY server resumed and enrolled")
	} else {
		fmt.Fprintln(out, "Registered WebTTY server created and enrolled")
	}
	fmt.Fprintf(out, "Project: %s\n", webTTYRegisteredServerProjectDescription(project))
	fmt.Fprintf(out, "Server ID: %s\n", response.Server.ID)
	fmt.Fprintf(out, "Status: active\n")
	fmt.Fprintf(out, "Local enrollment: %s\n", enrollmentPath)
	if enrollment != nil {
		fmt.Fprintf(out, "Fingerprint: %s\n", enrollment.ServerFingerprint)
		printWebTTYServerEnrollmentWorkspaceTrust(out, enrollment)
	}
	fmt.Fprintf(out, "Run command: rstream webtty server -v --server-id %s --login-user <local-username>\n", response.Server.ID)
}

func webTTYServerEnrollmentCommandOutput(runtime *resolvedRuntime, project webTTYRegisteredServerProject, response controlplane.CreateWebTTYServerResponse) map[string]any {
	output := map[string]any{
		"server":         response.Server,
		"enroll_command": webTTYServerEnrollCommand(runtime, project.ID, response.Server.ID),
		"run_command":    "rstream webtty server -v --server-id " + response.Server.ID + " --login-user <local-username>",
		"project":        project,
	}
	if runtime != nil && runtime.Resolved.APIURL != "" {
		output["api_url"] = runtime.Resolved.APIURL
	}
	return output
}

func webTTYServerEnrollCommand(runtime *resolvedRuntime, projectID string, serverID string) string {
	prefix := "rstream"
	if runtime != nil && config.NormalizeAPIURL(runtime.Resolved.APIURL) != "" && config.NormalizeAPIURL(runtime.Resolved.APIURL) != config.DefaultAPIURL() {
		prefix += " --api-url " + shellQuote(runtime.Resolved.APIURL)
	}
	command := fmt.Sprintf("%s webtty server enroll %s", prefix, shellQuote(serverID))
	if strings.TrimSpace(projectID) != "" {
		command += " --project-id " + shellQuote(projectID)
	}
	return command
}

func webTTYRegisteredServerProjectDescription(project webTTYRegisteredServerProject) string {
	if project.Source == "active_project" && project.ProjectEndpoint != "" {
		return fmt.Sprintf("%s (from active project %s)", project.ID, project.ProjectEndpoint)
	}
	return project.ID
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == ':' || r == '/' || r == '@' || r == '+' || r == '=' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
