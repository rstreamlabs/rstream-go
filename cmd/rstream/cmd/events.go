// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/spf13/cobra"
)

var eventsCmd = &cobra.Command{
	GroupID: "management",
	Use:     "events",
	Short:   "Watches and forwards webhook events",
	Run: func(cmd *cobra.Command, args []string) {
		// TODO
	},
}

func init() {
	eventsCmd.Flags().SortFlags = false
	eventsCmd.PersistentFlags().SortFlags = false
	eventsCmd.Flags().StringSliceP("events", "e", []string{}, "A comma-separated list of events to listen for")
	eventsCmd.Flags().StringP("forward-to", "t", "", "A URL to forward the webhook events to")
	rootCmd.AddCommand(eventsCmd)
}
