// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	GroupID: "common",
	Use:     "login",
	Short:   "Authenticate with the rstream engine",
	Run:     func(cmd *cobra.Command, args []string) {},
}

func init() {
	loginCmd.Flags().SortFlags = false
	loginCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(loginCmd)
}
