// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/spf13/cobra"
)

var (
	clientFilter string
	clientFormat string
	clientQuiet  bool
)

var clientsCmd = &cobra.Command{
	GroupID: "management",
	Use:     "clients",
	Short:   "List clients",
	Run:     func(cmd *cobra.Command, args []string) {},
}

func init() {
	clientsCmd.Flags().SortFlags = false
	clientsCmd.PersistentFlags().SortFlags = false
	clientsCmd.Flags().StringVarP(&clientFilter, "filter", "f", "", "Filter output based on conditions provided")
	clientsCmd.Flags().StringVar(&clientFormat, "format", "table", "Format output using a template (table or json)")
	clientsCmd.Flags().BoolVarP(&clientQuiet, "quiet", "q", false, "Only display client IDs")
	rootCmd.AddCommand(clientsCmd)
}
