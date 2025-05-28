// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/spf13/cobra"
)

var (
	tunnelFilter string
	tunnelFormat string
	tunnelQuiet  bool
)

var tunnelsCmd = &cobra.Command{
	GroupID: "management",
	Use:     "tunnels",
	Short:   "List tunnels",
	Run:     func(cmd *cobra.Command, args []string) {},
}

func init() {
	tunnelsCmd.Flags().SortFlags = false
	tunnelsCmd.PersistentFlags().SortFlags = false
	tunnelsCmd.Flags().StringVarP(&tunnelFilter, "filter", "f", "", "Filter output based on conditions provided")
	tunnelsCmd.Flags().StringVar(&tunnelFormat, "format", "table", "Format output using a template (table or json)")
	tunnelsCmd.Flags().BoolVarP(&tunnelQuiet, "quiet", "q", false, "Only display tunnel IDs")
	rootCmd.AddCommand(tunnelsCmd)
}
