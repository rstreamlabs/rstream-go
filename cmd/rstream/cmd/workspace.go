// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rstreamlabs/rstream-go/controlplane"
	"github.com/spf13/cobra"
)

var workspaceCmd = &cobra.Command{
	GroupID:      "management",
	Use:          "workspace",
	Short:        "Manage workspaces",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var workspaceListCmd = &cobra.Command{
	Use:          "list",
	Short:        "List workspaces",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runWorkspaceList(cmd)
	},
}

func init() {
	workspaceCmd.Flags().SortFlags = false
	workspaceCmd.PersistentFlags().SortFlags = false
	workspaceListCmd.Flags().SortFlags = false
	workspaceListCmd.Flags().String("type", "", "workspace type (personal, organization)")
	workspaceListCmd.Flags().String("membership-status", "", "membership status (active, invited)")
	workspaceListCmd.Flags().StringP("output", "o", "table", "output mode (table, json, yaml)")
	workspaceCmd.AddCommand(workspaceListCmd)
	rootCmd.AddCommand(workspaceCmd)
}

func runWorkspaceList(cmd *cobra.Command) error {
	runtime, err := resolveRuntime(cmd, false, true)
	if err != nil {
		return err
	}
	params, err := listWorkspacesParamsFromFlags(cmd)
	if err != nil {
		return err
	}
	client := newRuntimeControlPlaneClient(runtime.Resolved)
	if err := client.RequireToken(); err != nil {
		return err
	}
	resp, err := client.ListWorkspaces(cmd.Context(), params)
	if err != nil {
		return mapControlPlaneError(err)
	}
	output, _ := cmd.Flags().GetString("output")
	switch output {
	case "table":
		workspaces := append([]controlplane.Workspace(nil), resp.Workspaces...)
		sort.SliceStable(workspaces, func(i, j int) bool {
			if workspaces[i].Type == workspaces[j].Type {
				if workspaces[i].Name == workspaces[j].Name {
					return workspaces[i].ID < workspaces[j].ID
				}
				return workspaces[i].Name < workspaces[j].Name
			}
			return workspaceTypeRank(workspaces[i].Type) < workspaceTypeRank(workspaces[j].Type)
		})
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "NAME\tTYPE\tROLE\tSTATUS\tPROTECTION\tID")
		for _, workspace := range workspaces {
			role := "-"
			status := "-"
			if workspace.Membership != nil {
				role = workspace.Membership.Role
				status = workspace.Membership.Status
			}
			protection := workspaceProtectionSummary(workspace)
			_, _ = fmt.Fprintf(
				w,
				"%s\t%s\t%s\t%s\t%s\t%s\n",
				terminalSafeDefault(workspace.Name),
				terminalSafeDefault(workspace.Type),
				terminalSafeDefault(role),
				terminalSafeDefault(status),
				terminalSafeDefault(protection),
				terminalSafeDefault(workspace.ID),
			)
		}
		return w.Flush()
	case "json", "yaml":
		return writeStructuredOutput(output, resp)
	default:
		return validateOutputMode(output, "table", "json", "yaml")
	}
}

func workspaceTypeRank(value string) int {
	switch value {
	case "personal":
		return 0
	case "organization":
		return 1
	default:
		return 2
	}
}

func listWorkspacesParamsFromFlags(cmd *cobra.Command) (controlplane.ListWorkspacesParams, error) {
	var params controlplane.ListWorkspacesParams
	if value, _ := cmd.Flags().GetString("type"); strings.TrimSpace(value) != "" {
		value = strings.TrimSpace(value)
		if value != "personal" && value != "organization" {
			return params, errors.New("--type must be one of: personal, organization")
		}
		params.Type = value
	}
	if value, _ := cmd.Flags().GetString("membership-status"); strings.TrimSpace(value) != "" {
		value = strings.TrimSpace(value)
		if value != "active" && value != "invited" {
			return params, errors.New("--membership-status must be one of: active, invited")
		}
		params.MembershipStatus = value
	}
	return params, nil
}

func workspaceProtectionSummary(workspace controlplane.Workspace) string {
	if workspace.Enterprise == nil {
		return "-"
	}
	if workspace.Enterprise.WorkspaceKeyMode != "" {
		return workspace.Enterprise.WorkspaceKeyMode
	}
	if workspace.Enterprise.Status != "" {
		return workspace.Enterprise.Status
	}
	return "available"
}
