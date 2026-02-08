// See LICENSE file in the project root for license information.

package cmd

import (
	"errors"

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
		_, err := resolveControlPlane(cmd, true)
		if err != nil {
			return err
		}
		return errors.New("project list is not implemented")
	},
}

var projectUseCmd = &cobra.Command{
	Use:          "use <project-endpoint>",
	Short:        "Set the active project",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := resolveControlPlane(cmd, true)
		if err != nil {
			return err
		}
		return errors.New("project use is not implemented")
	},
}

func init() {
	projectCmd.Flags().SortFlags = false
	projectCmd.PersistentFlags().SortFlags = false
	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectUseCmd)
	projectListCmd.Flags().SortFlags = false
	projectListCmd.Flags().String("filter", "", "filter output")
	projectListCmd.Flags().StringP("output", "o", "table", "output mode (table, json, yaml)")
	projectListCmd.Flags().BoolP("quiet", "q", false, "only display project endpoints")
	projectUseCmd.Flags().SortFlags = false
	projectUseCmd.Flags().String("name", "", "context name (defaults to a derived name)")
	projectUseCmd.Flags().Bool("default", false, "set context as default")
	projectUseCmd.Flags().String("engine", "", "override engine URL (host:port)")
	rootCmd.AddCommand(projectCmd)
}
