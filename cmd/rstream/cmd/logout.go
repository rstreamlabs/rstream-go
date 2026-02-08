// See LICENSE file in the project root for license information.

package cmd

import (
	"github.com/rstreamlabs/rstream-go/cmd/rstream/internal/config"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	GroupID:      "common",
	Use:          "logout",
	Short:        "Logout of rstream",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, cfg, err := loadConfig(cmd)
		if err != nil {
			return err
		}
		apiURL, err := resolveAPIURL(cmd, cfg)
		if err != nil {
			return err
		}
		env, _ := cfg.FindEnvironment(apiURL)
		if env != nil {
			clearEnvironmentToken(env)
		}
		return config.WriteAtomic(path, cfg)
	},
}

func init() {
	logoutCmd.Flags().SortFlags = false
	logoutCmd.PersistentFlags().SortFlags = false
	rootCmd.AddCommand(logoutCmd)
}
