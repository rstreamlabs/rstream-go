// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/rstreamlabs/rstream-go/config"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "login [token]",
	Short:        "Login to rstream",
	Long:         "Provide a token via argument, stdin, or token file to authenticate and store it in the config.",
	SilenceUsage: true,
	Args:         cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		token, err := readTokenInput(cmd, args)
		if err != nil {
			return err
		}
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		if err := validateToken(cmd.Context(), apiURL, token); err != nil {
			return err
		}
		env := cfg.EnsureEnvironment(apiURL)
		if err := setEnvironmentToken(env, token); err != nil {
			return err
		}
		return config.WriteAtomic(path, cfg)
	},
}

func init() {
	loginCmd.Flags().SortFlags = false
	loginCmd.PersistentFlags().SortFlags = false
	loginCmd.Flags().Bool("stdin", false, "read token from stdin")
	loginCmd.Flags().String("token-file", "", "read token from file")
	loginCmd.MarkFlagsMutuallyExclusive("stdin", "token-file")
	rootCmd.AddCommand(loginCmd)
}
