// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "login",
	Short:        "Authenticate with the rstream engine",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newClientFromFlags(cmd)
		if err != nil {
			return err
		}
		host, err := client.Login(context.Background())
		if err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
		fmt.Fprintf(os.Stdout, "Logged in for host %s\n", *host)
		return nil
	},
}

func init() {
	loginCmd.Flags().SortFlags = false
	loginCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(loginCmd)
}
