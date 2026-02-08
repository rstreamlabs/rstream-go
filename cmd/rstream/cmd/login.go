// See LICENSE file in the project root for license information.

package cmd

import "github.com/spf13/cobra"

var loginCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "login [token]",
	Short:        "Login to rstream",
	Long:         "If a token is provided (argument, stdin, or token file), it is used directly. Otherwise, the default workflow opens a browser-based login.",
	SilenceUsage: true,
	Args:         cobra.MaximumNArgs(1),
	RunE:         func(cmd *cobra.Command, args []string) error { return nil },
}

func init() {
	loginCmd.Flags().SortFlags = false
	loginCmd.PersistentFlags().SortFlags = false
	loginCmd.Flags().Bool("stdin", false, "read token from stdin")
	loginCmd.Flags().String("token-file", "", "read token from file")
	loginCmd.MarkFlagsMutuallyExclusive("stdin", "token-file")
	rootCmd.AddCommand(loginCmd)
}
