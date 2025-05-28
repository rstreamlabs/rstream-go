// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	GroupID: "common",
	Use:     "logout",
	Short:   "Log out from the rstream engine",
	Run:     func(cmd *cobra.Command, args []string) {},
}

func init() {
	logoutCmd.Flags().SortFlags = false
	logoutCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(logoutCmd)
}
