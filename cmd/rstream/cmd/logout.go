// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "logout",
	Short:        "Log out from the rstream engine",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newClientFromFlags(cmd)
		if err != nil {
			return err
		}
		host, err := client.Logout(context.Background())
		if err != nil {
			return fmt.Errorf("logout failed: %w", err)
		}
		fmt.Fprintf(os.Stdout, "Logged out from host %s\n", *host)
		return nil
	},
}

func init() {
	logoutCmd.Flags().SortFlags = false
	logoutCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(logoutCmd)
}
