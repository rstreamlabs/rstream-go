// See LICENSE file in the project root for license information.

package cmd

import "github.com/spf13/cobra"

var logoutCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "logout",
	Short:        "Log out from rstream",
	Long:         "Delete locally stored authentication material.",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE:         func(cmd *cobra.Command, args []string) error { return nil },
}

func init() {
	logoutCmd.Flags().SortFlags = false
	logoutCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(logoutCmd)
}
