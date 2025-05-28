// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	GroupID: "management",
	Use:     "inspect [object-id]",
	Short:   "Inspect the details of an rstream object",
	Args:    cobra.ExactArgs(1),
	Run:     func(cmd *cobra.Command, args []string) {},
}

func init() {
	inspectCmd.Flags().SortFlags = false
	inspectCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(inspectCmd)
}
