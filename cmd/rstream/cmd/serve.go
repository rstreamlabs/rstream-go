// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	GroupID: "common",
	Use:     "serve [path]",
	Short:   "Serve a local directory or file",
	Args:    cobra.MaximumNArgs(1),
	Run:     func(cmd *cobra.Command, args []string) {},
}

func init() {
	serveCmd.Flags().SortFlags = false
	serveCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(serveCmd)
}
